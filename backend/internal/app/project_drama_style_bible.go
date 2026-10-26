// 导演风格（style.bible，M7.2 第 4 阶段）：项目级步骤。用户在章节页点「导演风格」，或剧本优化跑完自动接着跑；服务端按项目记忆
// （需求、画风、风格档案、全部剧集的场次表、已有风格锚）组装提示词、建一个任务，worker 在任务里一轮写一集（逐轮续写机制与剧本优化共用
// project_drama_run.go），收尾时解析、检查，把全局风格锚和逐场色彩脚本合并写进 bible_json（其余字段原样保留），每场建一张色卡资产
// （分类 palette，直接已确认，重跑同一场加版本），带了图片任务模板时接着给色卡排出图任务（project_drama_palette.go）。
// 项目还没有剧集时按原文章节定风格（一章当一集）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// DramaImageTaskTemplate 是给色卡出图用的图片任务模板：前端按项目默认图片模型组好的任务（模型选择、input.config 等，16:9），
// 服务端每张色卡换上自己的提示词和 metadata 后建任务。它放在任务 input 的顶层 paletteImageTask，任务结束后随密钥一起从任务正文去掉。
type DramaImageTaskTemplate struct {
	Model          string         `json:"model"`
	LogicalModelID string         `json:"logicalModelId,omitempty"`
	Provider       string         `json:"provider,omitempty"`
	Input          map[string]any `json:"input"`
}

func (t *DramaImageTaskTemplate) usable() bool {
	return t != nil && (strings.TrimSpace(t.Model) != "" || strings.TrimSpace(t.LogicalModelID) != "")
}

// dramaStyleMeta 是任务 metadata.dramaStyle：这次定风格的各集与场次（收尾检查用），有没有色卡出图，是不是剧本优化接着跑的。
type dramaStyleMeta struct {
	Episodes     []dramaStyleEpisodeMeta `json:"episodes"`
	FromChapters bool                    `json:"fromChapters,omitempty"`
	Images       bool                    `json:"images"`
	// ExcerptChars 是每场摘录的字数（总字数超预算时缩短，记下来便于排查）。
	ExcerptChars int `json:"excerptChars"`
	// TotalEpisodes 是项目现在一共几集（或几章）：已有色彩脚本里集号超过它的（剧集删了、重新改编成更少的集）合并时去掉。
	TotalEpisodes int    `json:"totalEpisodes"`
	ChainedFrom   string `json:"chainedFrom,omitempty"`
}

type dramaStyleEpisodeMeta struct {
	No     int      `json:"no"`
	UnitID string   `json:"unitId"`
	Title  string   `json:"title"`
	Scenes []string `json:"scenes"`
}

func (m *dramaStyleMeta) episodes() []drama.StyleBibleEpisode {
	out := make([]drama.StyleBibleEpisode, 0, len(m.Episodes))
	for _, ep := range m.Episodes {
		scenes := make([]drama.StyleBibleScene, 0, len(ep.Scenes))
		for i, h := range ep.Scenes {
			scenes = append(scenes, drama.StyleBibleScene{No: i + 1, Heading: h})
		}
		out = append(out, drama.StyleBibleEpisode{No: ep.No, UnitID: ep.UnitID, Title: ep.Title, Scenes: scenes})
	}
	return out
}

// DramaStyleBibleOutcome 是导演风格收尾写进任务结果 result.dramaStep 的摘要。
type DramaStyleBibleOutcome struct {
	Step     string                     `json:"step"`
	Status   string                     `json:"status"`
	Episodes int                        `json:"episodes"`
	Scenes   int                        `json:"scenes"`
	Parse    drama.StyleBibleParseStats `json:"parse"`
	Errors   int                        `json:"errors"`
	Warnings int                        `json:"warnings"`
	Items    []drama.CheckItem          `json:"items,omitempty"`
	// Palettes 是写出的色卡：Created 新建，Versioned 已有色卡加了版本。
	Palettes DramaPaletteWriteSummary `json:"palettes"`
	// Images 是色卡出图：排上的、等空位的（同时任务数有上限，前面的出完图自动接着排）、没排上的。
	Images DramaPaletteQueueResult `json:"images"`
}

// DramaPaletteWriteSummary 里 Retired 是标成场次已不存在的旧色卡（这次运行范围内、色彩脚本里不再有的场）。
type DramaPaletteWriteSummary struct {
	Created   int `json:"created"`
	Versioned int `json:"versioned"`
	Retired   int `json:"retired"`
}

// planStyleBible 选剧集（没有剧集时取原文章节）、切场、读已有风格锚和色彩脚本，组装提示词。
func (s *Service) planStyleBible(project *model.Project, step drama.Step, req DramaProjectStepRunRequest) (*dramaStepPlan, error) {
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].Position < units[j].Position })
	selected, numbers, fromChapters, err := styleBibleUnits(units, req.SourceUnitIDs)
	if err != nil {
		return nil, err
	}
	usage, skill, err := s.dramaStepSkill(project, step)
	if err != nil {
		return nil, err
	}
	return styleBiblePlan(project, selected, numbers, fromChapters, usage, skill)
}

// styleBibleUnits 是这次定风格的单元：全部剧集（按项目顺序，集号是在剧集里的序号）；项目还没有剧集时取原文章节。
// 点名的要属于这一类。
func styleBibleUnits(units []model.ProjectUnit, requested []string) ([]model.ProjectUnit, map[string]int, bool, error) {
	pick := func(kind model.ProjectUnitKind) []model.ProjectUnit {
		var out []model.ProjectUnit
		for _, u := range units {
			if u.Kind == kind {
				out = append(out, u)
			}
		}
		return out
	}
	all, fromChapters := pick(model.ProjectUnitKindEpisode), false
	if len(all) == 0 {
		all, fromChapters = pick(model.ProjectUnitKindChapter), true
	}
	if len(all) == 0 {
		return nil, nil, false, BadAuthRequest("项目里还没有剧集或章节，先导入小说或剧本")
	}
	numbers := map[string]int{}
	for i, u := range all {
		numbers[u.ID] = i + 1
	}
	if len(requested) == 0 {
		return all, numbers, fromChapters, nil
	}
	want := map[string]bool{}
	for _, id := range requested {
		want[strings.TrimSpace(id)] = true
	}
	var out []model.ProjectUnit
	for _, u := range all {
		if want[u.ID] {
			out = append(out, u)
			delete(want, u.ID)
		}
	}
	if len(want) > 0 {
		if fromChapters {
			return nil, nil, false, BadAuthRequest("所选单元不是这个项目的原文章节")
		}
		return nil, nil, false, BadAuthRequest("所选单元不是这个项目的剧集")
	}
	return out, numbers, fromChapters, nil
}

// styleBibleSceneRefs 是项目当前的场次表（总览「风格定了」「色卡齐了」的基准）：选单元、编集号、切场都与导演风格同一套
// （全部剧集，没有剧集时取原文章节，一章当一集）。项目里没有剧集也没有章节时返回空。
func styleBibleSceneRefs(units []model.ProjectUnit) ([]drama.SceneRef, bool) {
	sorted := slices.Clone(units)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	all, numbers, fromChapters, err := styleBibleUnits(sorted, nil)
	if err != nil {
		return nil, false
	}
	var out []drama.SceneRef
	for _, u := range all {
		for _, sc := range drama.StyleBibleScenes(chapterPlainText(u.SourceText), u.Title, drama.StyleBibleMinExcerptChars) {
			out = append(out, drama.SceneRef{Episode: numbers[u.ID], SceneNo: sc.No})
		}
	}
	return out, fromChapters
}

// styleBiblePlan 把选好的单元、项目记忆和技能版本组装成提示词与运行参数（不读库，评测直接拿评测集的单元喂进来）。
func styleBiblePlan(project *model.Project, units []model.ProjectUnit, numbers map[string]int, fromChapters bool, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	if len(units) > drama.StyleBibleMaxEpisodes {
		return nil, BadAuthRequest(fmt.Sprintf("一次最多给 %d 集定风格，请分几次跑", drama.StyleBibleMaxEpisodes))
	}
	texts := make([]string, len(units))
	total := 0
	for i, u := range units {
		texts[i] = chapterPlainText(u.SourceText)
		total += len(drama.StyleBibleScenes(texts[i], u.Title, drama.StyleBibleSceneExcerptChars))
	}
	limit := drama.StyleBibleExcerptLimit(total)
	episodes := make([]drama.StyleBibleEpisode, 0, len(units))
	meta := &dramaStyleMeta{FromChapters: fromChapters, ExcerptChars: limit, TotalEpisodes: len(numbers)}
	records := make([]map[string]any, 0, len(units))
	runEpisodes := map[int]bool{}
	for i, u := range units {
		scenes := drama.StyleBibleScenes(texts[i], u.Title, limit)
		if len(scenes) == 0 {
			return nil, BadAuthRequest(fmt.Sprintf("「%s」没有正文", u.Title))
		}
		no := numbers[u.ID]
		episodes = append(episodes, drama.StyleBibleEpisode{No: no, UnitID: u.ID, Title: u.Title, Scenes: scenes})
		headings := make([]string, 0, len(scenes))
		for _, sc := range scenes {
			headings = append(headings, sc.Heading)
		}
		meta.Episodes = append(meta.Episodes, dramaStyleEpisodeMeta{No: no, UnitID: u.ID, Title: u.Title, Scenes: headings})
		records = append(records, map[string]any{"unitId": u.ID, "title": u.Title, "kind": string(u.Kind), "episode": no})
		runEpisodes[no] = true
	}
	anchor, script := projectStyleBible(project.BibleJSON)
	existing := keptColorScript(script, runEpisodes, len(numbers))
	style := projectStyleSnapshot(project)
	in := drama.StyleBibleInput{
		Episodes: episodes, FromChapters: fromChapters, BriefJSON: project.BriefJSON,
		Style:  drama.StepStyle{Title: style.Title, Prompt: style.Prompt, Selection: style.Selection},
		Anchor: anchor, Existing: existing, Skill: skill,
	}
	if p, err := drama.BuiltinProfile(projectDramaContext(project).profileID()); err == nil {
		in.Profile = drama.StyleBibleProfile{Label: p.Label, StyleBlock: p.StyleBlock.Zh, LightPolicy: p.LightPolicy}
	}
	prompt, err := drama.BuildStyleBiblePrompt(in)
	if err != nil {
		return nil, err
	}
	return &dramaStepPlan{
		usage: usage, prompt: prompt, skill: skill,
		scenes: drama.StyleBibleTurns(episodes),
		record: map[string]any{"requirements": "输出全局风格锚和逐场色彩脚本，每场一行，供色卡、资产和分镜使用。", "dramaSources": records},
		style:  meta,
	}, nil
}

// keptColorScript 是这次不重写、合并时保留的已有色彩脚本：不在这次运行的集里，集号也没超过项目现在的集数（total 为 0 时不按集数筛）。
func keptColorScript(script []drama.ColorScriptEntry, runEpisodes map[int]bool, total int) []drama.ColorScriptEntry {
	var out []drama.ColorScriptEntry
	for _, e := range script {
		if !runEpisodes[e.Episode] && (total <= 0 || e.Episode <= total) {
			out = append(out, e)
		}
	}
	return out
}

// projectStyleBible 读 bible_json 里的风格锚和色彩脚本（读不出的字段当作没有）。
func projectStyleBible(bibleJSON string) (*drama.StyleAnchor, []drama.ColorScriptEntry) {
	var doc struct {
		StyleAnchor *drama.StyleAnchor       `json:"style_anchor"`
		ColorScript []drama.ColorScriptEntry `json:"color_script"`
	}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		if json.Unmarshal([]byte(raw), &doc) != nil {
			// 形状不对的字段（手改坏了）分开读，别让一个字段连累另一个。
			var loose map[string]json.RawMessage
			_ = json.Unmarshal([]byte(raw), &loose)
			_ = json.Unmarshal(loose["style_anchor"], &doc.StyleAnchor)
			_ = json.Unmarshal(loose["color_script"], &doc.ColorScript)
		}
	}
	return doc.StyleAnchor, doc.ColorScript
}

// ---------------------------------------------------------------- 收尾

// taskDramaStyle 从任务输入读 metadata.dramaStyle。
func taskDramaStyle(inputJSON string) *dramaStyleMeta {
	var input struct {
		Metadata struct {
			DramaStyle *dramaStyleMeta `json:"dramaStyle"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaStyle
}

// taskPaletteImageTemplate 从（解密后的）任务输入读顶层 paletteImageTask。
func taskPaletteImageTemplate(inputJSON string) *DramaImageTaskTemplate {
	var input struct {
		Template *DramaImageTaskTemplate `json:"paletteImageTask"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil || !input.Template.usable() {
		return nil
	}
	return input.Template
}

// applyStyleBibleTask 是导演风格的 Apply：模型输出 → 风格锚与色彩脚本 → 检查 → 合并写进 bible_json 并建 / 加版本色卡（同一事务）
// → 带了图片任务模板时排色卡出图。检查有 error 时不写库、任务失败（bible 和色卡都不动）；出图排不上不影响已写的结果。
func (s *Service) applyStyleBibleTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaStyleBibleOutcome, error) {
	style := taskDramaStyle(task.InputJSON)
	var input struct {
		Metadata struct {
			ProjectID string `json:"domainProjectId"`
		} `json:"metadata"`
	}
	_ = json.Unmarshal([]byte(task.InputJSON), &input)
	projectID := firstNonEmpty(strings.TrimSpace(input.Metadata.ProjectID), strings.TrimSpace(task.ProjectID))
	if style == nil || projectID == "" || len(style.Episodes) == 0 {
		return DramaStyleBibleOutcome{}, errors.New("导演风格任务缺少项目或运行参数")
	}
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return DramaStyleBibleOutcome{}, err
	}
	text, _ := result["text"].(string)
	out, stats := drama.StyleBibleFromText(text, 0)
	runEpisodes := map[int]bool{}
	for _, ep := range style.Episodes {
		runEpisodes[ep.No] = true
	}
	_, current := projectStyleBible(project.BibleJSON)
	existing := keptColorScript(current, runEpisodes, style.TotalEpisodes)
	report := drama.CheckStyleBible(out, drama.StyleBibleCheckContext{Episodes: style.episodes(), Existing: existing})
	if report.Errors > 0 {
		for _, item := range report.Items {
			if item.Level == "error" {
				where := ""
				if item.ShotID != "" {
					where = item.ShotID + " "
				}
				return DramaStyleBibleOutcome{}, fmt.Errorf("导演风格检查有 %d 个问题，没有写入：%s%s", report.Errors, where, item.Message)
			}
		}
	}
	entries := styleBibleRunEntries(out.Script, runEpisodes)
	merged, err := mergeStyleBibleDocument(project.BibleJSON, out.Anchor, existing, entries)
	if err != nil {
		return DramaStyleBibleOutcome{}, err
	}
	decrypted, decryptErr := s.decryptTaskInputJSON(task.InputJSON)
	template := (*DramaImageTaskTemplate)(nil)
	if style.Images && decryptErr == nil {
		template = taskPaletteImageTemplate(decrypted)
	}
	// 运行范围：这次重写的集，和超出项目现在集数的集（keptColorScript 合并时去掉的）；其中这次没写到的场，色卡标成场次已不存在。
	retire := func(m paletteMeta) bool {
		return runEpisodes[m.Episode] || (style.TotalEpisodes > 0 && m.Episode > style.TotalEpisodes)
	}
	writes, summary, err := s.dramaPaletteWrites(task.UserID, project, entries, template != nil, retire)
	if err != nil {
		return DramaStyleBibleOutcome{}, err
	}
	if err := s.repo.ApplyDramaStyleBible(task.UserID, project.ID, merged, writes, time.Now()); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaStyleBibleOutcome{}, errors.New("项目或色卡在写入前被删除，没有写入")
		}
		return DramaStyleBibleOutcome{}, err
	}
	outcome := DramaStyleBibleOutcome{Step: meta.Step, Status: DramaStepOutcomeWritten, Episodes: len(style.Episodes), Scenes: len(entries), Parse: stats, Errors: report.Errors, Warnings: report.Warnings, Palettes: summary, Images: DramaPaletteQueueResult{Queued: []string{}, Waiting: []string{}, Failed: []string{}}}
	for _, item := range report.Items {
		if len(outcome.Items) < dramaAdaptOutcomeItems {
			outcome.Items = append(outcome.Items, item)
		}
	}
	if style.Images && template == nil {
		_ = s.log(task.UserID, task.ID, "warn", "色卡出图没有排上", "任务里读不出图片任务模板")
	}
	if template != nil {
		if outcome.Images, err = s.queueDramaPaletteImages(task.UserID, project.ID, *template, task.ID, nil); err != nil {
			_ = s.log(task.UserID, task.ID, "warn", "色卡出图没有排上", err.Error())
			outcome.Images = DramaPaletteQueueResult{Queued: []string{}, Waiting: []string{}, Failed: []string{}}
		}
	}
	return outcome, nil
}

// styleBibleRunEntries 是这次要写的场：只取这次运行的各集，同一场写了两次取第一次，按集号、场序排好。
func styleBibleRunEntries(script []drama.ColorScriptEntry, runEpisodes map[int]bool) []drama.ColorScriptEntry {
	seen := map[string]bool{}
	var out []drama.ColorScriptEntry
	for _, e := range script {
		if !runEpisodes[e.Episode] || seen[e.Code()] {
			continue
		}
		seen[e.Code()] = true
		out = append(out, e)
	}
	sortColorScript(out)
	return out
}

func sortColorScript(entries []drama.ColorScriptEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Episode != entries[j].Episode {
			return entries[i].Episode < entries[j].Episode
		}
		return entries[i].SceneNo < entries[j].SceneNo
	})
}

// mergeStyleBibleDocument 把风格锚和色彩脚本合并进 bible_json：风格锚按字段覆盖（这次没写的字段保留旧值），色彩脚本 = 这次没重写的各集 +
// 这次写的各集；其余字段原样保留。合并结果走与 UpdateProjectBible 同一套形状校验。
func mergeStyleBibleDocument(bibleJSON string, anchor *drama.StyleAnchor, existing []drama.ColorScriptEntry, entries []drama.ColorScriptEntry) (string, error) {
	doc := map[string]any{}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("项目的连戏表不是合法 JSON，没有写入导演风格：%w", err)
		}
	}
	merged := map[string]any{}
	if old, ok := doc["style_anchor"].(map[string]any); ok {
		maps.Copy(merged, old)
	}
	if anchor != nil {
		encoded, _ := json.Marshal(anchor)
		var fresh map[string]any
		_ = json.Unmarshal(encoded, &fresh)
		maps.Copy(merged, fresh)
	}
	doc["style_anchor"] = merged
	script := append(slices.Clone(existing), entries...)
	sortColorScript(script)
	encodedScript, _ := json.Marshal(script)
	var list []any
	_ = json.Unmarshal(encodedScript, &list)
	if list == nil {
		list = []any{}
	}
	doc["color_script"] = list
	if err := validateDramaBibleDocument(doc); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(doc)
	return string(encoded), err
}

// ---------------------------------------------------------------- 剧本优化跑完自动接着跑

// DramaChainedTask 是收尾自动接着建的下一步任务（result.dramaStep.chained）；Error 是没建成的原因（不影响这一步已写的结果）。
type DramaChainedTask struct {
	Step   string `json:"step"`
	TaskID string `json:"taskId,omitempty"`
	Error  string `json:"error,omitempty"`
}

// chainStyleBibleAfterAdapt 在剧本优化写完剧集后，用同一文本模型直接建导演风格任务（metadata.chainedFrom = 剧本优化任务）；
// 剧本优化请求带了色卡图片模板时一并带上。项目的风格档案拿不到导演风格的技能（没绑定、默认技能也不在）时不接。
func (s *Service) chainStyleBibleAfterAdapt(task model.Task, projectID string) *DramaChainedTask {
	step, ok := drama.StepByID(drama.StepStyleBible)
	if !ok {
		return nil
	}
	chained := &DramaChainedTask{Step: step.ID}
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		chained.Error = err.Error()
		return chained
	}
	if _, _, err := s.dramaStepSkill(project, step); err != nil {
		return nil
	}
	decrypted, err := s.decryptTaskInputJSON(task.InputJSON)
	if err != nil {
		chained.Error = err.Error()
		return chained
	}
	var input struct {
		Config map[string]any `json:"config"`
	}
	_ = json.Unmarshal([]byte(decrypted), &input)
	req := DramaProjectStepRunRequest{Model: task.Model, LogicalModelID: task.LogicalModelID, Config: input.Config, PaletteImages: taskPaletteImageTemplate(decrypted), TraceID: task.TraceID, chainedFrom: task.ID}
	result, err := s.RunDramaProjectStep(task.UserID, projectID, step.ID, req)
	if err != nil {
		chained.Error = userFacingTaskError(err.Error())
		return chained
	}
	chained.TaskID = result.Task.ID
	return chained
}
