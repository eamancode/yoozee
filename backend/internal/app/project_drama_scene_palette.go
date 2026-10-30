// 场次色卡（scene.palette，M8 卡 B，由 M7.2 导演风格 style.bible 改来）：项目级步骤。剧本优化写完剧集后服务端自动接着建任务
// （metadata.chainedFrom），也可以在章节页点「生成场次色卡」。项目要先锁定风格（风格锚 + 主基调色卡，style.lock 写的）：服务端按
// 锁定的风格锚、主基调色卡、风格档案、全部剧集的场次表和这次不重写的已有色彩脚本组装提示词，建一个任务，worker 在任务里一轮写一集
// （逐轮续写机制与剧本优化共用 project_drama_run.go）。收尾时解析、检查，只把逐场色彩脚本合并写进 bible_json.color_script
// （风格锚、主基调色卡和其余字段原样保留），每场建 / 加版本一张场次色卡资产（project_drama_palette.go），再由代码合成色卡图绑上，
// 不调图片模型。项目还没有剧集时按原文章节做（一章当一集）。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// dramaScenePaletteMeta 是任务 metadata.dramaScenePalette：这次写色卡的各集与场次（收尾检查用），是不是剧本优化接着建的。
type dramaScenePaletteMeta struct {
	Episodes     []dramaScenePaletteEpisodeMeta `json:"episodes"`
	FromChapters bool                           `json:"fromChapters,omitempty"`
	// ExcerptChars 是每场摘录的字数（总字数超预算时缩短，记下来便于排查）。
	ExcerptChars int    `json:"excerptChars"`
	ChainedFrom  string `json:"chainedFrom,omitempty"`
}

type dramaScenePaletteEpisodeMeta struct {
	No     int      `json:"no"`
	UnitID string   `json:"unitId"`
	Title  string   `json:"title"`
	Scenes []string `json:"scenes"`
}

func (m *dramaScenePaletteMeta) episodes() []drama.ScenePaletteEpisode {
	out := make([]drama.ScenePaletteEpisode, 0, len(m.Episodes))
	for _, ep := range m.Episodes {
		scenes := make([]drama.ScenePaletteScene, 0, len(ep.Scenes))
		for i, h := range ep.Scenes {
			scenes = append(scenes, drama.ScenePaletteScene{No: i + 1, Heading: h})
		}
		out = append(out, drama.ScenePaletteEpisode{No: ep.No, UnitID: ep.UnitID, Title: ep.Title, Scenes: scenes})
	}
	return out
}

// DramaScenePaletteOutcome 是场次色卡收尾写进任务结果 result.dramaStep 的摘要。
type DramaScenePaletteOutcome struct {
	Step     string                       `json:"step"`
	Status   string                       `json:"status"`
	Episodes int                          `json:"episodes"`
	Scenes   int                          `json:"scenes"`
	Parse    drama.ScenePaletteParseStats `json:"parse"`
	Errors   int                          `json:"errors"`
	Warnings int                          `json:"warnings"`
	Items    []drama.CheckItem            `json:"items,omitempty"`
	// Palettes 是写出的色卡：Created 新建，Versioned 已有色卡加了版本，Retired 标成场次已不存在。
	Palettes DramaPaletteWriteSummary `json:"palettes"`
	// Rendered 是合成并绑上图的色卡张数；ImageFailures 是没合成上图的（资产页「重新合成」可以补）。
	Rendered      int                   `json:"rendered"`
	ImageFailures []DramaPaletteFailure `json:"imageFailures,omitempty"`
	// Chained 是写完后自动接着起的第一集提取资产（M8 卡 D）。
	Chained *DramaChainedTask `json:"chained,omitempty"`
}

// DramaPaletteWriteSummary 里 Retired 是标成场次已不存在的旧色卡（这次运行范围内、色彩脚本里不再有的场）。
type DramaPaletteWriteSummary struct {
	Created   int `json:"created"`
	Versioned int `json:"versioned"`
	Retired   int `json:"retired"`
}

// errStyleNotLocked 是项目还没锁定风格时生成场次色卡的提示。
const errStyleNotLocked = "项目还没有锁定风格：场次色卡以锁定的风格锚和主基调色卡为参考，先在章节页「分析项目风格」锁定风格，或建项目时选风格库"

// planScenePalette 要求项目已锁定风格，选剧集（没有剧集时取原文章节）、切场、读已有色彩脚本，组装提示词。
func (s *Service) planScenePalette(project *model.Project, step drama.Step, req DramaProjectStepRunRequest) (*dramaStepPlan, error) {
	if _, _, _, locked := projectLockedStyle(project.BibleJSON); !locked {
		return nil, BadAuthRequest(errStyleNotLocked)
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].Position < units[j].Position })
	selected, numbers, fromChapters, err := styleBibleUnits(units, req.SourceUnitIDs)
	if err != nil {
		return nil, err
	}
	usage, skill, err := s.dramaStepSkill(project, step, nil)
	if err != nil {
		return nil, err
	}
	return scenePalettePlan(project, selected, numbers, fromChapters, styleSceneTable(units), usage, skill)
}

// scenePalettePlan 把选好的单元、项目锁定的风格和技能版本组装成提示词与运行参数（不读库，评测直接拿评测集的单元喂进来，table 为 nil）。
func scenePalettePlan(project *model.Project, units []model.ProjectUnit, numbers map[string]int, fromChapters bool, table map[string]styleSceneSlot, usage DramaStepUsage, skill drama.StepSkill) (*dramaStepPlan, error) {
	if len(units) > drama.ScenePaletteMaxEpisodes {
		return nil, BadAuthRequest(fmt.Sprintf("一次最多给 %d 集做场次色卡，请分几次跑", drama.ScenePaletteMaxEpisodes))
	}
	texts := make([]string, len(units))
	total := 0
	for i, u := range units {
		texts[i] = chapterPlainText(u.SourceText)
		total += len(drama.ScenePaletteScenes(texts[i], u.Title, drama.ScenePaletteSceneExcerptChars))
	}
	limit := drama.ScenePaletteExcerptLimit(total)
	episodes := make([]drama.ScenePaletteEpisode, 0, len(units))
	meta := &dramaScenePaletteMeta{FromChapters: fromChapters, ExcerptChars: limit}
	records := make([]map[string]any, 0, len(units))
	runEpisodes := map[int]bool{}
	for i, u := range units {
		scenes := drama.ScenePaletteScenes(texts[i], u.Title, limit)
		if len(scenes) == 0 {
			return nil, BadAuthRequest(fmt.Sprintf("「%s」没有正文", u.Title))
		}
		no := numbers[u.ID]
		episodes = append(episodes, drama.ScenePaletteEpisode{No: no, UnitID: u.ID, Title: u.Title, Scenes: scenes})
		headings := make([]string, 0, len(scenes))
		for _, sc := range scenes {
			headings = append(headings, sc.Heading)
		}
		meta.Episodes = append(meta.Episodes, dramaScenePaletteEpisodeMeta{No: no, UnitID: u.ID, Title: u.Title, Scenes: headings})
		records = append(records, map[string]any{"unitId": u.ID, "title": u.Title, "kind": string(u.Kind), "episode": no})
		runEpisodes[no] = true
	}
	anchor, master, _, _ := projectLockedStyle(project.BibleJSON)
	_, script := projectStyleBible(project.BibleJSON)
	in := drama.ScenePaletteInput{
		Episodes: episodes, FromChapters: fromChapters,
		Anchor: anchor, Master: master, Existing: keptColorScript(script, runEpisodes, table), Skill: skill,
	}
	if p, err := drama.BuiltinProfile(projectDramaContext(project).profileID()); err == nil {
		in.Profile = drama.StyleBibleProfile{Label: p.Label, StyleBlock: p.StyleBlock.Zh, LightPolicy: p.LightPolicy}
	}
	prompt, err := drama.BuildScenePalettePrompt(in)
	if err != nil {
		return nil, err
	}
	return &dramaStepPlan{
		usage: usage, prompt: prompt, skill: skill,
		scenes:  drama.ScenePaletteTurns(episodes),
		record:  map[string]any{"requirements": "以锁定的风格锚和主基调色卡为参考，逐场写色彩脚本，每场一行，供场次色卡、资产和视频使用。", "dramaSources": records},
		palette: meta,
	}, nil
}

// styleBibleUnits 是这次写场次色卡的单元（也是项目场次表的单元）：全部剧集（按项目顺序，集号是在剧集里的序号）；项目还没有剧集时取原文章节。
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

// styleBibleSceneRefs 是项目当前的场次表（总览「风格定了」「色卡齐了」的基准）：选单元、编集号、切场都与场次色卡同一套
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
		for _, sc := range drama.ScenePaletteScenes(chapterPlainText(u.SourceText), u.Title, drama.ScenePaletteMinExcerptChars) {
			out = append(out, drama.SceneRef{Episode: numbers[u.ID], SceneNo: sc.No})
		}
	}
	return out, fromChapters
}

// styleSceneSlot 是当前场次表里的一场：所在单元和场次头。LegacyShifted 是切场规则改成「连续同地点并成一场」后，
// 规则改变前写的同编号数据（一个场次头一场）已经不是这一场了（前面有并起来的场，编号往前挪了）。
type styleSceneSlot struct {
	UnitID        string
	Heading       string
	LegacyShifted bool
}

// styleSceneTable 是项目当前的场次表，按场编号（选单元、编集号、切场与场次色卡同一套）。项目里没有剧集也没有章节时返回空表。
func styleSceneTable(units []model.ProjectUnit) map[string]styleSceneSlot {
	sorted := slices.Clone(units)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Position < sorted[j].Position })
	table := map[string]styleSceneSlot{}
	all, numbers, _, err := styleBibleUnits(sorted, nil)
	if err != nil {
		return table
	}
	for _, u := range all {
		for _, sc := range drama.SplitScenes(chapterPlainText(u.SourceText), u.Title) {
			table[drama.SceneCode(numbers[u.ID], sc.No)] = styleSceneSlot{UnitID: u.ID, Heading: sc.Heading, LegacyShifted: !slices.Contains(sc.LegacyNos, sc.No)}
		}
	}
	return table
}

// colorScriptCurrent 是这一场的色彩脚本还对得上当前场次表：场编号还在，且写它时所在的单元就是现在这一场的单元
// （改编出剧集后，原文章节版同编号的条目对不上）；没记单元的旧条目按场次头对。切场规则改变前写的条目，编号错位了也对不上
// （要重新生成色彩脚本，或用 RenumberDramaScenes 按新场次重排）。table 为 nil（评测，不知道项目的场次）时都算对得上。
func colorScriptCurrent(e drama.ColorScriptEntry, table map[string]styleSceneSlot) bool {
	if table == nil {
		return true
	}
	slot, ok := table[e.Code()]
	if !ok || (e.SceneRule == 0 && slot.LegacyShifted) {
		return false
	}
	if e.UnitID != "" {
		return e.UnitID == slot.UnitID
	}
	return e.Scene == "" || strings.Join(strings.Fields(e.Scene), "") == strings.Join(strings.Fields(slot.Heading), "")
}

// scenePaletteRunEntries 是这次要写的场：这次运行的各集里、场次表上有的场（同一场写了两次取第一次），场次头、单元和切场规则
// 由服务端按建任务时的场次表填（模型只写场编号）。runScenes 是场编号 → 场次头，runUnits 是集号 → 单元。
func scenePaletteRunEntries(script []drama.ColorScriptEntry, runEpisodes map[int]bool, runScenes map[string]string, runUnits map[int]string) []drama.ColorScriptEntry {
	var out []drama.ColorScriptEntry
	for _, e := range styleBibleRunEntries(script, runEpisodes) {
		heading, ok := runScenes[e.Code()]
		if !ok {
			continue
		}
		e.Scene, e.UnitID, e.SceneRule = heading, runUnits[e.Episode], drama.SceneRuleMerged
		out = append(out, e)
	}
	return out
}

// keptColorScript 是这次不重写、合并时保留的已有色彩脚本：不在这次运行的集里，且还对得上当前场次表（colorScriptCurrent：
// 集删了、场少了、剧集写出来以后原文章节版同编号的条目，合并时都去掉）。
func keptColorScript(script []drama.ColorScriptEntry, runEpisodes map[int]bool, table map[string]styleSceneSlot) []drama.ColorScriptEntry {
	var out []drama.ColorScriptEntry
	for _, e := range script {
		if !runEpisodes[e.Episode] && colorScriptCurrent(e, table) {
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

// taskDramaScenePalette 从任务输入读 metadata.dramaScenePalette。
func taskDramaScenePalette(inputJSON string) *dramaScenePaletteMeta {
	var input struct {
		Metadata struct {
			DramaScenePalette *dramaScenePaletteMeta `json:"dramaScenePalette"`
		} `json:"metadata"`
	}
	if json.Unmarshal([]byte(inputJSON), &input) != nil {
		return nil
	}
	return input.Metadata.DramaScenePalette
}

// applyScenePaletteTask 是场次色卡的 Apply：模型输出 → 色彩脚本 → 检查 → 合并写进 bible_json.color_script 并建 / 加版本场次色卡
// （同一事务）→ 每张写了版本的色卡由代码合成图绑上。检查有 error 时不写库、任务失败（bible 和色卡都不动）；合成图没绑上的记在结果里，
// 不影响已写的色彩脚本（资产页「重新合成」可以补）。风格锚和主基调色卡只读不写。
func (s *Service) applyScenePaletteTask(task model.Task, meta *dramaStepTaskMeta, result map[string]interface{}) (DramaScenePaletteOutcome, error) {
	run := taskDramaScenePalette(task.InputJSON)
	projectID := taskProjectID(task)
	if run == nil || projectID == "" || len(run.Episodes) == 0 {
		return DramaScenePaletteOutcome{}, errors.New("场次色卡任务缺少项目或运行参数")
	}
	project, err := s.activeProjectForUser(task.UserID, projectID)
	if err != nil {
		return DramaScenePaletteOutcome{}, err
	}
	units, err := s.repo.ProjectUnits(project.ID)
	if err != nil {
		return DramaScenePaletteOutcome{}, err
	}
	text, _ := result["text"].(string)
	out, stats := drama.ScenePaletteFromText(text, 0)
	runEpisodes, runUnits, runScenes := map[int]bool{}, map[int]string{}, map[string]string{}
	for _, ep := range run.Episodes {
		runEpisodes[ep.No], runUnits[ep.No] = true, ep.UnitID
		for i, heading := range ep.Scenes {
			runScenes[drama.SceneCode(ep.No, i+1)] = heading
		}
	}
	table := styleSceneTable(units)
	_, current := projectStyleBible(project.BibleJSON)
	existing := keptColorScript(current, runEpisodes, table)
	_, master, _, _ := projectLockedStyle(project.BibleJSON)
	report := drama.CheckScenePalette(out, drama.ScenePaletteCheckContext{Episodes: run.episodes(), Existing: existing, Master: master})
	if report.Errors > 0 {
		for _, item := range report.Items {
			if item.Level == "error" {
				where := ""
				if item.ShotID != "" {
					where = item.ShotID + " "
				}
				return DramaScenePaletteOutcome{}, fmt.Errorf("场次色卡检查有 %d 个问题，没有写入：%s%s", report.Errors, where, item.Message)
			}
		}
	}
	entries := scenePaletteRunEntries(out.Script, runEpisodes, runScenes, runUnits)
	if len(entries) == 0 {
		return DramaScenePaletteOutcome{}, errors.New("场次色卡没有一场对得上这次的场次表，没有写入")
	}
	// 别的集里切场规则改变前写的、编号错位的条目原样留着（不当承接用），色卡也不标。
	keptAll := append(slices.Clone(existing), legacyShiftedKept(current, runEpisodes, units, table)...)
	sortColorScript(keptAll)
	// 先按读到的快照合并一次（形状不合规就不写）；真正写库时在事务里按当前的 bible_json 再合并，只替换 color_script。
	if _, err := mergeColorScriptDocument(project.BibleJSON, keptAll, entries); err != nil {
		return DramaScenePaletteOutcome{}, err
	}
	patch := func(bibleJSON string) (string, error) { return mergeColorScriptDocument(bibleJSON, keptAll, entries) }
	// 合并后色彩脚本里不再有的场（这次重写的集里没写到的，和 keptColorScript 去掉的），色卡标成场次已不存在。
	kept := map[string]bool{}
	for _, e := range keptAll {
		kept[e.Code()] = true
	}
	retire := func(m paletteMeta) bool { return !kept[m.Code] }
	writes, summary, err := s.dramaPaletteWrites(task.UserID, project, entries, retire)
	if err != nil {
		return DramaScenePaletteOutcome{}, err
	}
	if err := s.repo.ApplyDramaScenePalettes(task.UserID, project.ID, patch, writes, time.Now()); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaScenePaletteOutcome{}, errors.New("项目或色卡在写入前被删除，没有写入")
		}
		return DramaScenePaletteOutcome{}, err
	}
	outcome := DramaScenePaletteOutcome{Step: meta.Step, Status: DramaStepOutcomeWritten, Episodes: len(run.Episodes), Scenes: len(entries), Parse: stats, Errors: report.Errors, Warnings: report.Warnings, Palettes: summary}
	for _, item := range report.Items {
		if len(outcome.Items) < dramaAdaptOutcomeItems {
			outcome.Items = append(outcome.Items, item)
		}
	}
	byCode := map[string]drama.ColorScriptEntry{}
	for _, e := range entries {
		byCode[e.Code()] = e
	}
	for _, w := range writes {
		if w.Version == nil {
			continue
		}
		e := byCode[paletteCodeOf(w.Asset)]
		if err := s.renderScenePaletteImage(task.UserID, project.ID, w.Asset.ID, w.Version.ID, e); err != nil {
			outcome.ImageFailures = append(outcome.ImageFailures, DramaPaletteFailure{AssetID: w.Asset.ID, Code: e.Code(), Error: userFacingTaskError(err.Error())})
			_ = s.log(task.UserID, task.ID, "warn", "场次色卡图没有合成上："+e.Code(), err.Error())
			continue
		}
		outcome.Rendered++
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

// mergeColorScriptDocument 把色彩脚本合并进 bible_json：color_script = 这次没重写的各集 + 这次写的各集；风格锚、主基调色卡和
// 其余字段原样保留。合并结果走与 UpdateProjectBible 同一套形状校验。
func mergeColorScriptDocument(bibleJSON string, existing []drama.ColorScriptEntry, entries []drama.ColorScriptEntry) (string, error) {
	doc := map[string]any{}
	if raw := strings.TrimSpace(bibleJSON); raw != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("项目的连戏表不是合法 JSON，没有写入场次色卡：%w", err)
		}
	}
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
// UnitID / Remaining 是接着提取资产时这一个任务提取的单元和后面还排着的集数。
type DramaChainedTask struct {
	Step      string `json:"step"`
	TaskID    string `json:"taskId,omitempty"`
	UnitID    string `json:"unitId,omitempty"`
	Remaining int    `json:"remaining,omitempty"`
	Error     string `json:"error,omitempty"`
}
