// 镜头表落库（yoozee 2.1）：分镜行 + spec → 剧本台词表 → 镜头表 → 检查 → 装段 → 写段版本。
// 镜头表是段的来源，段是派生物；用户改镜头表就整章重装段。领域规则在 internal/drama。

package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"

	"gorm.io/gorm"
)

const (
	dramaShotlistMaxRows = 200
	// 一个分镜行的 spec（镜头表的一镜）上限，防止把整章塞进一行。
	dramaShotlistRowMaxBytes = 32 << 10
)

// DramaShotlistRule 是渠道实际能提交的段长（秒）：叠加在项目生成模型的上限上，只能收紧不能放宽。
type DramaShotlistRule struct {
	MaxSeconds int `json:"maxSeconds"`
	MinSeconds int `json:"minSeconds"`
}

type DramaShotlistWriteRequest struct {
	// Rows 是分镜行（前端 StoryboardRow），带或不带 spec。
	Rows            []json.RawMessage  `json:"rows"`
	Rule            *DramaShotlistRule `json:"rule,omitempty"`
	ExpectedShotIDs []string           `json:"expectedShotIds,omitempty"`
}

// DramaShotlistView 是一章的镜头表、台词表与最近一次检查结果。
type DramaShotlistView struct {
	UnitID    string            `json:"unitId"`
	Version   int               `json:"version"`
	Shotlist  drama.Shotlist    `json:"shotlist"`
	Script    *drama.Script     `json:"script,omitempty"`
	Check     drama.CheckReport `json:"check"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type DramaShotlistWriteResult struct {
	Shotlist DramaShotlistView  `json:"shotlist"`
	Segments []DramaSegmentView `json:"segments"`
	Check    drama.CheckReport  `json:"check"`
	Stats    drama.RowsStats    `json:"stats"`
	// Notes 是装段说明：找不到合法切点的场、渠道时长等，给用户看。
	Notes []string `json:"notes"`
}

// shotlistRow 是分镜行里服务端会读的字段（spec 之外的平铺字段只用来补段版本的备注）。
type shotlistRow struct {
	drama.StoryboardRow
	Characters []struct {
		CharacterName    string `json:"characterName"`
		CharacterAssetID string `json:"characterAssetId"`
	} `json:"characters"`
	VideoMotionPrompt     string `json:"videoMotionPrompt"`
	ImageGenerationPrompt string `json:"imageGenerationPrompt"`
	NegativePrompt        string `json:"negativePrompt"`
}

func (r shotlistRow) assetIDs() []string {
	var ids []string
	for _, b := range r.AssetBindings {
		if id := strings.TrimSpace(b.NodeID); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, c := range r.Characters {
		if id := strings.TrimSpace(c.CharacterAssetID); id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	return ids[:min(6, len(ids))]
}

var (
	htmlBlockBreak = regexp.MustCompile(`(?i)<\s*(br\s*/?|/p|/div|/h[1-6]|/li|/blockquote|/pre)\s*>`)
	htmlTag        = regexp.MustCompile(`<[^>]*>`)
)

// chapterPlainText 把章节正文（编辑器保存的 HTML）还原成逐行纯文本：段落、换行、标题各成一行。
func chapterPlainText(source string) string {
	text := htmlBlockBreak.ReplaceAllString(source, "\n")
	text = htmlTag.ReplaceAllString(text, "")
	return strings.TrimSpace(html.UnescapeString(text))
}

func shotlistCheckFailed(report drama.CheckReport, stats drama.RowsStats) error {
	var first string
	for _, item := range report.Items {
		if item.Level == "error" {
			first = item.Message
			break
		}
	}
	err := NewAppError(http.StatusBadRequest, fmt.Sprintf("镜头表检查有 %d 个 error，没有保存：%s", report.Errors, first))
	err.Reason = ReasonShotlistCheckFailed
	err.Details = map[string]any{"check": report, "stats": stats}
	return err
}

// dramaAssetDescription 是资产的文字设定（与前端资产清单同一来源）：预览文字，没有就取设定里的描述类字段。
func dramaAssetDescription(a ProjectAssetSummary) string {
	if text := strings.Join(strings.Fields(a.PreviewText), " "); text != "" {
		return text
	}
	var definition map[string]any
	if a.Character != nil {
		definition = a.Character.Definition
	} else if a.Visual != nil {
		definition = a.Visual.Definition
	}
	for _, key := range []string{"appearance", "外貌", "外观", "description", "描述", "prompt"} {
		if text, ok := definition[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.Join(strings.Fields(text), " ")
		}
	}
	return ""
}

var dramaLockSplit = regexp.MustCompile(`[，,；;。、\n]+`)

// dramaLockCard 把文字设定拆成外貌声明的短句（最多 6 条，与 pack.py 的 cast 取 lock_card 前 6 条一致）。
func dramaLockCard(description string) []string {
	var out []string
	for _, part := range dramaLockSplit.Split(description, -1) {
		if part = strings.TrimSpace(part); part != "" && len(out) < 6 {
			out = append(out, string([]rune(part)[:min(40, len([]rune(part)))]))
		}
	}
	return out
}

// dramaImagedAssets 是有图可挂的资产：资产主版本绑了图片表示（三视图、正面、主图），人物卡有图，或资产本身是上传的图片。
// 只有它们能成为段的参考（编译成 @图片N）；只有文字设定的人物写外貌声明，场景、道具写进镜头描述。与前端提交视频时取参考图的规则一致。
func (s *Service) dramaImagedAssets(assets []ProjectAssetSummary) (map[string]bool, error) {
	versions := make([]string, 0, len(assets))
	for _, a := range assets {
		if a.PrimaryVersionID != "" {
			versions = append(versions, a.PrimaryVersionID)
		}
	}
	representations, err := s.repo.AssetRepresentationsByVersionIDs(versions)
	if err != nil {
		return nil, err
	}
	imagedVersions := map[string]bool{}
	for _, r := range representations {
		if r.ResourceID != "" && strings.HasPrefix(r.MediaType, "image") {
			imagedVersions[r.AssetVersionID] = true
		}
	}
	out := make(map[string]bool, len(assets))
	for _, a := range assets {
		out[a.ID] = dramaAssetHasImage(a, imagedVersions[a.PrimaryVersionID])
	}
	return out, nil
}

func dramaAssetHasImage(a ProjectAssetSummary, versionHasImage bool) bool {
	if a.PrimaryVersionID == "" {
		return false
	}
	if versionHasImage || (a.MediaType == "image" && strings.TrimSpace(a.StorageKey) != "") {
		return true
	}
	if a.Character != nil {
		for _, r := range a.Character.Representations {
			if r.ResourceID != "" && strings.HasPrefix(r.MediaType, "image") {
				return true
			}
		}
	}
	return false
}

// dramaShotlistBible 用分镜行绑定的项目资产拼一份最小资产圣经：资产标题即人物 / 场景 / 道具名，有图的资产文件是主版本 ID，
// 镜头 refs_hint 里写的「名称·变体」也挂到同一个版本上，装段按 refs_hint 取参考时就能落到柚子资产。
// 没图的人物照样进圣经但不给文件，锁定卡取文字设定：装段就不挂参考，改写「人物·X」外貌声明（pack.py 身份图缺失时同样处理）；
// 没图的场景、道具不进圣经，由 describeTextOnlyAssets 写进镜头描述。
func dramaShotlistBible(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary, imaged map[string]bool) *drama.Bible {
	byID := make(map[string]ProjectAssetSummary, len(assets))
	for _, a := range assets {
		byID[a.ID] = a
	}
	bible := &drama.Bible{}
	added := map[string]bool{}
	variants := map[string][]string{}
	for _, sh := range shots {
		for _, hint := range sh.RefsHint {
			if name, variant, ok := strings.Cut(hint, "·"); ok && variant != "" && !slices.Contains(variants[name], variant) {
				variants[name] = append(variants[name], variant)
			}
		}
	}
	for i := range rows {
		for _, id := range rows[i].assetIDs() {
			a, ok := byID[id]
			if !ok || a.PrimaryVersionID == "" || added[a.Title] || strings.TrimSpace(a.Title) == "" {
				continue
			}
			file := ""
			if imaged[a.ID] {
				file = a.PrimaryVersionID
			}
			switch dramaRefRole(a.Category) {
			case "identity":
				lock := dramaLockCard(dramaAssetDescription(a))
				ch := drama.BibleCharacter{Name: a.Title, Variants: []drama.BibleVariant{{Kind: "base", Sheet: file, LockCard: lock}}}
				for _, v := range variants[a.Title] {
					ch.Variants = append(ch.Variants, drama.BibleVariant{Label: v, Kind: "state", Sheet: file, LockCard: lock})
				}
				bible.Characters = append(bible.Characters, ch)
			case "location":
				if file == "" {
					continue
				}
				bible.Locations = append(bible.Locations, drama.BibleLocation{Name: a.Title, Plate: file})
			case "prop":
				if file == "" {
					continue
				}
				bible.Props = append(bible.Props, drama.BibleProp{Name: a.Title, Image: file})
			default:
				continue
			}
			added[a.Title] = true
		}
	}
	return bible
}

// describeTextOnlyAssets 把没图的场景、道具写进段里第一个用到它的镜头描述（「场景·电梯口（夜）：…。」），每段每个资产一次。
func describeTextOnlyAssets(segments []drama.Segment, shots []drama.Shot, assets []ProjectAssetSummary, imaged map[string]bool) {
	type textAsset struct{ kind, text string }
	byTitle := map[string]textAsset{}
	for _, a := range assets {
		if imaged[a.ID] || strings.TrimSpace(a.Title) == "" {
			continue
		}
		kind := map[string]string{"location": "场景", "prop": "道具"}[dramaRefRole(a.Category)]
		if desc := dramaAssetDescription(a); kind != "" && desc != "" {
			byTitle[strings.TrimSpace(a.Title)] = textAsset{kind, string([]rune(desc)[:min(80, len([]rune(desc)))])}
		}
	}
	hintsByShot := make(map[string][]string, len(shots))
	for _, sh := range shots {
		hintsByShot[sh.ID] = sh.RefsHint
	}
	for si := range segments {
		described := map[string]bool{}
		for k := range segments[si].Shots {
			sub := &segments[si].Shots[k]
			var add []string
			for _, hint := range hintsByShot[sub.ShotID] {
				name, _, _ := strings.Cut(hint, "·")
				if t, ok := byTitle[name]; ok && !described[name] {
					described[name] = true
					add = append(add, t.kind+"·"+name+"："+strings.TrimRight(t.text, "。")+"。")
				}
			}
			if len(add) > 0 {
				text := strings.TrimSpace(sub.Text)
				if text != "" && !strings.HasSuffix(text, "。") {
					text += "。"
				}
				sub.Text = text + strings.Join(add, "")
			}
		}
	}
}

// dramaLocationKey 去掉括注（「电梯口（夜）」→「电梯口」）再比场景名。
func dramaLocationKey(name string) string {
	if i := strings.IndexAny(name, "（("); i > 0 {
		name = name[:i]
	}
	return strings.TrimSpace(name)
}

// sceneOwnLocations 是每场自己的场景资产：剧本场标题（「内·电梯口·夜」）里写的地点能对上资产名就用它，
// 对不上就取这场镜头里列得最多的场景（平手取先出现的）。
func sceneOwnLocations(shots []drama.Shot, hints [][]string, isLocation func(string) bool, script *drama.Script) map[string]string {
	counts := map[string]map[string]int{}
	order := map[string][]string{}
	for i, sh := range shots {
		for _, h := range hints[i] {
			name, _, _ := strings.Cut(h, "·")
			if !isLocation(name) {
				continue
			}
			if counts[sh.Scene] == nil {
				counts[sh.Scene] = map[string]int{}
			}
			if counts[sh.Scene][name] == 0 {
				order[sh.Scene] = append(order[sh.Scene], name)
			}
			counts[sh.Scene][name]++
		}
	}
	out := map[string]string{}
	for scene, names := range order {
		if script != nil {
			for _, sc := range script.Scenes {
				if sc.ID != scene {
					continue
				}
				for _, part := range strings.Split(sc.Heading, "·") {
					for _, name := range names {
						if key := dramaLocationKey(name); key != "" && dramaLocationKey(part) == key {
							out[scene] = name
						}
					}
				}
			}
		}
		if out[scene] != "" {
			continue
		}
		best := names[0]
		for _, name := range names {
			if counts[scene][name] > counts[scene][best] {
				best = name
			}
		}
		out[scene] = best
	}
	return out
}

// attachRefsHints 让每镜的 refs_hint 带上这一行绑定的资产名：模型写的提示保留（「病历夹·掉在地上」进装段的状态句），
// 绑定了但没写进提示的资产补在后面，装段时一起挂成参考。
//   - 人物资产只补这一镜真在场的（站位、焦点、分镜行人物里有他），免得同一场景资产串起来的两场戏互相带人。
//   - 一镜只留一个场景：这一镜列了本场自己的场景（见 sceneOwnLocations）就留它，否则留这一镜列的第一个。
//     场景参考是整段生成的背景，一镜里顺口提到的别处（电梯口的戏里写到「身后是病房走廊」）不该再挂一张背景图；
//     一场里真的换了地方（走廊走进病房）的镜头只列新地方，照样挂得上。
func attachRefsHints(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary, script *drama.Script) {
	type boundAsset struct {
		title     string
		character bool
	}
	byID := make(map[string]boundAsset, len(assets))
	locations := map[string]bool{}
	for _, a := range assets {
		if a.PrimaryVersionID == "" {
			continue
		}
		title := strings.TrimSpace(a.Title)
		byID[a.ID] = boundAsset{title, dramaRefRole(a.Category) == "identity"}
		if dramaRefRole(a.Category) == "location" {
			locations[title] = true
		}
	}
	all := make([][]string, len(shots))
	for i := range shots {
		hints := slices.Clone([]string(shots[i].RefsHint))
		if i < len(rows) {
			named := map[string]bool{}
			for _, h := range hints {
				name, _, _ := strings.Cut(h, "·")
				named[name] = true
			}
			present := map[string]bool{shots[i].Focus: true}
			for _, b := range shots[i].Blocking {
				present[b.Who] = true
			}
			for _, c := range rows[i].Characters {
				present[strings.TrimSpace(c.CharacterName)] = true
			}
			for _, id := range rows[i].assetIDs() {
				a := byID[id]
				if a.title == "" || named[a.title] || (a.character && !present[a.title]) {
					continue
				}
				named[a.title] = true
				hints = append(hints, a.title)
			}
		}
		all[i] = hints
	}
	isLocation := func(name string) bool { return locations[name] }
	own := sceneOwnLocations(shots, all, isLocation, script)
	for i := range shots {
		keep := ""
		for _, h := range all[i] {
			name, _, _ := strings.Cut(h, "·")
			if !isLocation(name) {
				continue
			}
			if keep == "" || name == own[shots[i].Scene] {
				keep = name
			}
		}
		hints := all[i][:0:0]
		for _, h := range all[i] {
			name, _, _ := strings.Cut(h, "·")
			if isLocation(name) && name != keep {
				continue
			}
			hints = append(hints, h)
		}
		shots[i].RefsHint = hints
	}
}

func dramaShotlistModel(project *model.Project, rule *DramaShotlistRule) (drama.Model, error) {
	m, err := projectDramaModel(project)
	if err != nil {
		return drama.Model{}, err
	}
	if rule == nil {
		return m, nil
	}
	if rule.MaxSeconds > 0 && rule.MaxSeconds < m.Duration.Max {
		m.Duration.Max = rule.MaxSeconds
	}
	if rule.MinSeconds > m.Duration.Min && rule.MinSeconds <= m.Duration.Max {
		m.Duration.Min = rule.MinSeconds
	}
	if m.Duration.Max < m.Duration.Min {
		return drama.Model{}, BadAuthRequest("段长规则无效：最长秒数小于最短秒数")
	}
	return m, nil
}

func dramaCheckContext(script *drama.Script) (drama.CheckContext, *drama.Profile, error) {
	profile, err := drama.BuiltinProfile(drama.DefaultProfileID)
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	vocab, err := drama.BuiltinVocab()
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	rules, err := drama.BuiltinShotRules()
	if err != nil {
		return drama.CheckContext{}, nil, err
	}
	return drama.CheckContext{Script: script, Profile: profile, Vocab: vocab, Rules: rules}, profile, nil
}

// WriteDramaShotlist 把一章的分镜行转成镜头表、检查、装段并整章替换段：检查有 error 时整批拒绝，不落库。
func (s *Service) WriteDramaShotlist(userID string, projectID string, unitID string, req DramaShotlistWriteRequest) (DramaShotlistWriteResult, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	unitID = strings.TrimSpace(unitID)
	unit, err := s.repo.ProjectUnit(projectID, unitID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("章节 %q 不在这个项目里", unitID))
		}
		return DramaShotlistWriteResult{}, err
	}
	if len(req.Rows) == 0 || len(req.Rows) > dramaShotlistMaxRows {
		return DramaShotlistWriteResult{}, BadAuthRequest(fmt.Sprintf("分镜行数量必须在 1 到 %d 之间", dramaShotlistMaxRows))
	}
	rows, err := parseShotlistRows(req.Rows)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	m, err := dramaShotlistModel(project, req.Rule)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	var script *drama.Script
	if parsed := drama.ScriptFromText(chapterPlainText(unit.SourceText), unit.Title); len(parsed.Scenes) > 0 {
		script = &parsed
	}
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	imaged, err := s.dramaImagedAssets(assets)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	plan, err := planDramaShotlist(rows, script, assets, imaged, m)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	shotlist, report, segments, problems := plan.Shotlist, plan.Report, plan.Segments, plan.Problems
	rowByShot := make(map[string]shotlistRow, len(rows))
	for i, sh := range shotlist.Shots {
		rowByShot[sh.ID] = rows[i]
	}
	inputs := make([]ReplaceProjectUnitShotInput, 0, len(segments))
	for _, seg := range segments {
		input, err := dramaSegmentShotInput(seg, rowByShot)
		if err != nil {
			return DramaShotlistWriteResult{}, err
		}
		inputs = append(inputs, input)
	}
	// 5 镜头表与段同一事务写入
	shotlistJSON, _ := json.Marshal(shotlist)
	checkJSON, _ := json.Marshal(report)
	record := &model.DramaShotlist{ShotlistJSON: string(shotlistJSON), CheckJSON: string(checkJSON)}
	if script != nil {
		scriptJSON, _ := json.Marshal(script)
		record.ScriptJSON = string(scriptJSON)
	}
	if _, err := s.replaceProjectUnitShots(userID, projectID, unitID, ReplaceProjectUnitShotsRequest{Shots: inputs, ExpectedShotIDs: req.ExpectedShotIDs}, record); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	notes := append([]string{}, problems...)
	if script == nil {
		notes = append(notes, "章节正文为空，台词覆盖（R-SHOT-001）没有检查")
	}
	view := DramaShotlistView{UnitID: unitID, Version: record.Version, Shotlist: shotlist, Script: script, Check: report, UpdatedAt: record.UpdatedAt}
	return DramaShotlistWriteResult{Shotlist: view, Segments: views, Check: report, Stats: plan.Stats, Notes: notes}, nil
}

func parseShotlistRows(raws []json.RawMessage) ([]shotlistRow, error) {
	rows := make([]shotlistRow, 0, len(raws))
	for i, raw := range raws {
		var row shotlistRow
		if err := json.Unmarshal(raw, &row); err != nil {
			return nil, BadAuthRequest(fmt.Sprintf("第 %d 个分镜行格式无效：%s", i+1, err.Error()))
		}
		if len(row.Spec) > dramaShotlistRowMaxBytes {
			return nil, BadAuthRequest(fmt.Sprintf("第 %d 个分镜行的 spec 超过 %d KB", i+1, dramaShotlistRowMaxBytes>>10))
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// dramaShotlistPlan 是一章分镜行的处理结果：镜头表、检查、装出的段（还没落库）。
type dramaShotlistPlan struct {
	Shotlist drama.Shotlist
	Report   drama.CheckReport
	Stats    drama.RowsStats
	Segments []drama.Segment
	Problems []string
}

// planDramaShotlist 是写镜头表不碰数据库的部分：分镜行 → 镜头表 → 检查（有 error 返回 shotlist_check_failed）→ 按场装段。
// 场按镜头表的 scene（E01S01…）分，不按绑定的场景资产分：两场戏用同一个场景资产时各装各的段，参考取各自镜头。
func planDramaShotlist(rows []shotlistRow, script *drama.Script, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model) (dramaShotlistPlan, error) {
	storyRows := make([]drama.StoryboardRow, 0, len(rows))
	for _, row := range rows {
		storyRows = append(storyRows, row.StoryboardRow)
	}
	converted := drama.RowsToShotlist(storyRows, script, drama.DefaultProfileID)
	plan := dramaShotlistPlan{Shotlist: converted.Shotlist, Stats: converted.Stats}
	ctx, profile, err := dramaCheckContext(script)
	if err != nil {
		return plan, err
	}
	plan.Report = drama.CheckShots(plan.Shotlist, ctx)
	plan.Report.AddSchemaIssues(converted.SchemaIssues)
	if plan.Report.HasErrors() {
		return plan, shotlistCheckFailed(plan.Report, converted.Stats)
	}
	packShots := slices.Clone(plan.Shotlist.Shots)
	attachRefsHints(packShots, rows, assets, script)
	bible := dramaShotlistBible(packShots, rows, assets, imaged)
	plan.Segments, plan.Problems = drama.Pack(drama.PackInput{
		Shotlist: drama.Shotlist{Profile: plan.Shotlist.Profile, Episode: plan.Shotlist.Episode, Shots: packShots},
		Script:   script, Bible: bible, Model: m, Profile: profile, ProfileID: plan.Shotlist.Profile, Vocab: ctx.Vocab,
		FileExists: func(string) bool { return true },
	})
	describeTextOnlyAssets(plan.Segments, packShots, assets, imaged)
	return plan, nil
}

// dramaSegmentShotInput 把装出的一段写成章节镜头：段 JSON 进版本 spec，平铺字段由它补齐，分镜行的提示词备注接在后面。
func dramaSegmentShotInput(seg drama.Segment, rowByShot map[string]shotlistRow) (ReplaceProjectUnitShotInput, error) {
	spec, err := json.Marshal(seg)
	if err != nil {
		return ReplaceProjectUnitShotInput{}, err
	}
	var texts, lines, videoPrompts []string
	for _, sh := range seg.Shots {
		texts = append(texts, sh.Text)
		for _, ln := range sh.Lines {
			lines = append(lines, ln.Speaker+"："+ln.Text)
		}
	}
	var imagePrompt, negative string
	var versions []string
	for _, id := range seg.ShotIDs {
		row := rowByShot[id]
		if p := strings.TrimSpace(row.VideoMotionPrompt); p != "" {
			videoPrompts = append(videoPrompts, p)
		}
		imagePrompt = firstNonEmpty(imagePrompt, strings.TrimSpace(row.ImageGenerationPrompt))
		negative = firstNonEmpty(negative, strings.TrimSpace(row.NegativePrompt))
	}
	for _, ref := range seg.Refs {
		if ref.Role != "motion" && !slices.Contains(versions, ref.File) {
			versions = append(versions, ref.File)
		}
	}
	description := strings.Join(texts, "；")
	title := seg.ID
	if seg.Title != "" {
		title += " " + seg.Title
	}
	durationMs := int64(seg.Dur * 1000)
	return ReplaceProjectUnitShotInput{
		CreateProjectShotRequest: CreateProjectShotRequest{Title: title, Description: description, DurationMs: durationMs, Revision: ShotRevisionInput{
			PlotDescription: description, Dialogue: strings.Join(lines, "\n"), DurationMs: durationMs,
			VideoPrompt: strings.Join(videoPrompts, "\n"), ImagePrompt: imagePrompt, NegativePrompt: negative, Spec: spec,
		}},
		AssetVersionIDs: versions[:min(6, len(versions))],
	}, nil
}

// DramaShotlist 读一章的镜头表、台词表与最近一次检查结果；这一章还没有镜头表时返回 nil。
func (s *Service) DramaShotlist(userID string, projectID string, unitID string) (*DramaShotlistView, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		return nil, err
	}
	unitID = strings.TrimSpace(unitID)
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		return nil, err
	}
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	view := &DramaShotlistView{UnitID: unitID, Version: record.Version, UpdatedAt: record.UpdatedAt}
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &view.Shotlist); err != nil {
		return nil, fmt.Errorf("镜头表数据损坏: %w", err)
	}
	if strings.TrimSpace(record.ScriptJSON) != "" {
		var script drama.Script
		if err := json.Unmarshal([]byte(record.ScriptJSON), &script); err == nil {
			view.Script = &script
		}
	}
	if strings.TrimSpace(record.CheckJSON) != "" {
		_ = json.Unmarshal([]byte(record.CheckJSON), &view.Check)
	}
	return view, nil
}

// dramaRefAsset 是段参考文件对应的项目资产：known=false 表示认不出（Agent 手写的文件名等），这种参考原样保留。
type dramaRefAsset struct {
	asset    ProjectAssetSummary
	known    bool
	hasImage bool
}

// dropImagelessRefs 去掉段里指向没图资产的参考：编译成「@图片N」却没有文件，提交时会缺图或错号。
// 去掉的人物改写「人物·X」外貌声明（文字设定拆成短句），场景、道具只靠镜头文字；一张参考都不剩时改成 text_only。
// 镜头表装段已经不会挂没图的资产，这里兜住旧段、前端装段和 Agent 写的段。
func dropImagelessRefs(seg drama.Segment, lookup func(file string) dramaRefAsset) (drama.Segment, []drama.Issue) {
	var issues []drama.Issue
	var kept []drama.Ref
	dropped := map[string]bool{}
	for _, r := range seg.Refs {
		info := lookup(r.File)
		if !info.known || info.hasImage || r.Role == "motion" {
			kept = append(kept, r)
			continue
		}
		dropped[r.File] = true
		title := firstNonEmpty(strings.TrimSpace(info.asset.Title), r.File)
		if r.Role == "identity" {
			lock := dramaLockCard(dramaAssetDescription(info.asset))
			if len(lock) > 0 && !slices.ContainsFunc(seg.Cast, func(c drama.CastLine) bool { return c.Name == title }) {
				seg.Cast = append(seg.Cast, drama.CastLine{Name: title, Lock: lock})
			}
			issues = append(issues, drama.Issue{Level: "warn", Message: fmt.Sprintf("%s 的人物「%s」还没有图，按文字外貌声明生成", seg.ID, title)})
		} else {
			issues = append(issues, drama.Issue{Level: "warn", Message: fmt.Sprintf("%s 的「%s」还没有图，不挂参考，只按镜头文字生成", seg.ID, title)})
		}
	}
	if len(dropped) == 0 {
		return seg, nil
	}
	seg.Refs = kept
	if b := seg.ContinuityBridge; b != nil {
		bridge := *b
		bridge.SharedRefs = slices.DeleteFunc(slices.Clone(bridge.SharedRefs), func(f string) bool { return dropped[f] })
		seg.ContinuityBridge = &bridge
	}
	if seg.Mode == drama.ModeLockIdentity && len(seg.Refs) == 0 && len(seg.Voices) == 0 {
		seg.Mode = drama.ModeTextOnly
	}
	return seg, issues
}

// dramaRefLookup 按资产 ID、主版本 ID 或人物卡版本 ID 认出段参考指向的项目资产，并判断这个版本有没有图。
func (s *Service) dramaRefLookup(userID string, projectID string, seg drama.Segment) (func(string) dramaRefAsset, error) {
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return nil, err
	}
	byFile := map[string]ProjectAssetSummary{}
	versions := []string{}
	for _, a := range assets {
		byFile[a.ID] = a
		if a.PrimaryVersionID != "" {
			byFile[a.PrimaryVersionID] = a
			versions = append(versions, a.PrimaryVersionID)
		}
		if a.Character != nil && a.Character.VersionID != "" {
			byFile[a.Character.VersionID] = a
		}
	}
	for _, r := range seg.Refs {
		versions = append(versions, r.File)
	}
	representations, err := s.repo.AssetRepresentationsByVersionIDs(versions)
	if err != nil {
		return nil, err
	}
	imagedVersions := map[string]bool{}
	for _, r := range representations {
		if r.ResourceID != "" && strings.HasPrefix(r.MediaType, "image") {
			imagedVersions[r.AssetVersionID] = true
		}
	}
	return func(file string) dramaRefAsset {
		a, ok := byFile[file]
		if !ok {
			return dramaRefAsset{}
		}
		return dramaRefAsset{asset: a, known: true, hasImage: imagedVersions[file] || dramaAssetHasImage(a, imagedVersions[a.PrimaryVersionID])}
	}, nil
}
