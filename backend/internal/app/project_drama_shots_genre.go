// 生成分段的逐场注入（M7.2 第 6 阶段）：建任务时把戏类判定的提示词、本单元每场的色卡段（导演风格色彩脚本里这一场的颜色）
// 和技能包里的四张戏类卡存进 metadata.dramaShots；worker 在第一轮之前用同一个模型判一次戏类（判定失败、判不出的场按文戏），
// 结果写进任务结果 result.dramaRun（genres 与 scenes[i].genre），之后每一轮接上本场色卡和本场戏类的卡。

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaShotsMeta 是任务 metadata.dramaShots。
type dramaShotsMeta struct {
	// GenrePrompt 是戏类判定那一次调用的提示词；GenreScenes 是要判的场（场编号，按顺序；没有场次头的正文是一场 E01S01）。
	GenrePrompt string   `json:"genrePrompt"`
	GenreScenes []string `json:"genreScenes"`
	// Palettes 是每场的【本场色卡】段，按场编号（导演风格没写到、对不上当前场次表的场没有）。
	Palettes map[string]string `json:"palettes,omitempty"`
	// Cards 是技能包里的戏类卡；老版本包没有戏类卡时为空，每一轮只写判定结果。
	Cards []drama.SceneGenreCard `json:"cards,omitempty"`
}

// chapterShotsSceneMeta 组装逐场注入的参数；正文切不出场时返回 nil（planDramaStep 已先拦下没有正文的单元）。
func chapterShotsSceneMeta(project *model.Project, unit *model.ProjectUnit, units []model.ProjectUnit, skill drama.StepSkill) *dramaShotsMeta {
	scenes := drama.ChapterShotsGenreScenes(chapterPlainText(unit.SourceText), unit.Title)
	if len(scenes) == 0 {
		return nil
	}
	meta := &dramaShotsMeta{GenrePrompt: drama.SceneGenrePrompt(unit.Title, scenes), Cards: drama.SceneGenreCards(skill), Palettes: map[string]string{}}
	script := unitSceneColorScript(project.BibleJSON, units, unit.ID)
	for _, sc := range scenes {
		meta.GenreScenes = append(meta.GenreScenes, sc.ID)
		if e, ok := script[sc.ID]; ok {
			if text := drama.ScenePalettePrompt(&e); text != "" {
				meta.Palettes[sc.ID] = text
			}
		}
	}
	return meta
}

// unitSceneColorScript 是这个单元各场的色彩脚本，按单元内的场编号（E01S02，与镜头 spec.scene 同一套）：
// 导演风格按项目统一编集号，这里取场次表里属于这个单元、还对得上当前场次表的条目。
func unitSceneColorScript(bibleJSON string, units []model.ProjectUnit, unitID string) map[string]drama.ColorScriptEntry {
	_, script := projectStyleBible(bibleJSON)
	if len(script) == 0 {
		return nil
	}
	table := styleSceneTable(units)
	out := map[string]drama.ColorScriptEntry{}
	for _, e := range script {
		if slot, ok := table[e.Code()]; ok && slot.UnitID == unitID && colorScriptCurrent(e, table) {
			out[drama.SceneCode(1, e.SceneNo)] = e
		}
	}
	return out
}

// taskDramaShots 从任务输入读 metadata.dramaShots；没有返回 nil。metadata 不含密钥，不用解密。
func taskDramaShots(inputJSON string) *dramaShotsMeta {
	if !strings.Contains(inputJSON, `"dramaShots"`) {
		return nil
	}
	var input struct {
		Metadata struct {
			DramaShots *dramaShotsMeta `json:"dramaShots"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || input.Metadata.DramaShots == nil || len(input.Metadata.DramaShots.GenreScenes) == 0 {
		return nil
	}
	return input.Metadata.DramaShots
}

// classifySceneGenres 是第一轮之前的戏类判定：同一个模型、不带对话历史、不进文本流，用一次分镜超时。调用失败各场按文戏；
// 只有任务本身被取消或到总时限时返回错误。
func (s *Service) classifySceneGenres(ctx context.Context, task model.Task, input canvasGenerationInput, shots *dramaShotsMeta, timeout time.Duration) ([]drama.SceneGenre, map[string]any, error) {
	call := input
	call.Prompt, call.TextHistory, call.OnTextDelta = shots.GenrePrompt, nil, func(string) {}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	result, err := runTextTask(callCtx, call)
	cancel()
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		_ = s.log(task.UserID, task.ID, "warn", "戏类判定失败，各场按文戏写", err.Error())
		return drama.FallbackSceneGenres(shots.GenreScenes, "戏类判定失败，按文戏"), nil, nil
	}
	text, _ := result["text"].(string)
	genres, recognized := drama.ParseSceneGenres(text, shots.GenreScenes)
	if recognized < len(genres) {
		_ = s.log(task.UserID, task.ID, "warn", fmt.Sprintf("戏类判定只判出 %d/%d 场，其余按文戏写", recognized, len(genres)), jsTruncateForLog(text))
	}
	usage, _ := result["usage"].(map[string]any)
	return genres, usage, nil
}

func jsTruncateForLog(text string) string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return string(runes)
}

// sceneGenreOf 是某一场的判定；没有判定（不该发生）时按文戏。
func sceneGenreOf(genres []drama.SceneGenre, scene string) drama.SceneGenre {
	for _, g := range genres {
		if g.Scene == scene {
			return g
		}
	}
	return drama.SceneGenre{Scene: scene, Genre: drama.SceneGenreFallback, Fallback: true}
}

// dramaShotsTurnScene 是第 k 轮写的场：逐场续写取轮次表，整章一轮时取正文唯一的一场。
func dramaShotsTurnScene(run dramaRunMeta, shots *dramaShotsMeta, k int) string {
	if k <= len(run.Scenes) {
		return run.Scenes[k-1].ID
	}
	if len(shots.GenreScenes) > 0 {
		return shots.GenreScenes[0]
	}
	return ""
}

// dramaShotsTurnExtra 是第 k 轮接在提示词后面的本场色卡和戏类段：这种戏类第一次出现时带整张卡，前面各轮已经给过就只提一句。
func dramaShotsTurnExtra(shots *dramaShotsMeta, genre drama.SceneGenre, prior []dramaRunTurn) string {
	var card *drama.SceneGenreCard
	for i := range shots.Cards {
		if shots.Cards[i].Genre == genre.Genre {
			card = &shots.Cards[i]
			break
		}
	}
	prompts := make([]string, 0, len(prior))
	for _, t := range prior {
		prompts = append(prompts, t.Prompt)
	}
	return joinTruthyStrings(shots.Palettes[genre.Scene], drama.SceneGenrePromptSection(genre, card, drama.SceneGenreCardGiven(prompts, genre.Genre)))
}

// withSceneGenres 是带上戏类的轮次表（进度里显示）。
func withSceneGenres(scenes []drama.ShotsScene, genres []drama.SceneGenre) []drama.ShotsScene {
	if len(genres) == 0 {
		return scenes
	}
	out := make([]drama.ShotsScene, len(scenes))
	for i, sc := range scenes {
		g := sceneGenreOf(genres, sc.ID)
		sc.Genre, sc.GenreReason = g.Genre, g.Reason
		out[i] = sc
	}
	return out
}

func joinTruthyStrings(parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "\n\n")
}

// unitScenePalettes 是装段时绑到各场镜头的色卡：本单元每一场（单元内场编号 E01S02）对上的色卡资产主版本。色卡按项目级场编号
// （导演风格的集号）认，对到场次表里属于本单元、色彩脚本还对得上当前场次表的场；场次已不存在（stale）、归档、没有主版本的跳过。
func (s *Service) unitScenePalettes(userID string, project *model.Project, unitID string) (map[string]string, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	script := unitSceneColorScript(project.BibleJSON, units, unitID)
	if len(script) == 0 {
		return nil, nil
	}
	assets, err := s.projectPaletteAssets(userID, project.ID)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for sceneID, e := range script {
		a, ok := assets[e.Code()]
		if !ok || paletteMetaOf(&a).Stale || a.PrimaryVersionID == "" || a.Status == model.AssetVersionStatusArchived {
			continue
		}
		out[sceneID] = a.PrimaryVersionID
	}
	return out, nil
}

// segmentPaletteVersions 是一段要绑的色卡版本：装段按场装，一段一场（Segment.Scene 是镜头的 spec.scene）。
func segmentPaletteVersions(seg drama.Segment, palettes map[string]string) []string {
	if v := palettes[strings.ToUpper(strings.TrimSpace(seg.Scene))]; v != "" {
		return []string{v}
	}
	return nil
}
