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
	// 一个分镜行的 spec（镜头表的一镜）上限，防止把整章塞进一行。
	dramaShotlistRowMaxBytes = 32 << 10
	// dramaShotlistMaxBytes 是一次写入的全部分镜行合计上限（Agent 整章重写也走这里）：上限章的镜头按每行十几 KB 算也放得下。
	dramaShotlistMaxBytes = 32 << 20
	// dramaShotEditMaxShots 是分镜卡一次最多改几个镜头（改到的段另有一次 60 段的上限）。
	dramaShotEditMaxShots = 200
)

// dramaChapterMaxShots 是一章镜头表的镜头数上限，也是一章段数的上限（一段至少一镜）：生成分段收尾、重新装段、Agent 整章重写共用。
// 按小节生成最密的是武戏一秒一镜，2000 镜约合 33 分钟一章，远超一集；建任务时先按各节估时拦下（chapterShotsOverCap），
// 不让模型写完了才在收尾被拒。测试里调小。
var dramaChapterMaxShots = 2000

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
	// KeepDraft 为 true 时检查有 error 也不拒绝：分镜行、镜头表和检查结果照样写进镜头表记录（草稿），
	// 但不装段、不动本章已有的段；用户处理完问题后用 repack 重新装段。生成分段的收尾用它。
	KeepDraft bool `json:"-"`
	// AcceptIssues 为 true 时是「先装段，到分镜制作再改」：检查有 error 也照样装段写库，检查结果记成已接受（check.accepted），
	// 问题按镜头挂到段上（DramaSegmentView.Issues）。会让装段本身做不成的问题（镜头时长不是正数、一场找不到合法切点
	// 只能整场装成一段而超过单段上限）仍然拒绝。
	AcceptIssues bool `json:"-"`
	// Parse 是服务端解析模型输出的统计，记进检查结果。
	Parse *drama.ShotLinesStats `json:"-"`
	// LegacyScenes 是重装切场规则改变前写的镜头表：台词表按旧规则（一个场次头一场）切，镜头保持原场编号，色卡按旧场所在的新场找。
	LegacyScenes bool `json:"-"`
	// Sections 是按小节生成时每一节的原样输出（存进 sections_json）；为 nil 时（重新装段、带问题装段、手写镜头表）沿用上一版存下的，
	// 新的镜头表已经不按小节（镜头没写 section）时清空。
	Sections []drama.SectionOutput `json:"-"`
}

// DramaShotlistView 是一章的镜头表、台词表与最近一次检查结果。
type DramaShotlistView struct {
	UnitID   string            `json:"unitId"`
	Version  int               `json:"version"`
	Shotlist drama.Shotlist    `json:"shotlist"`
	Script   *drama.Script     `json:"script,omitempty"`
	Check    drama.CheckReport `json:"check"`
	// Draft 表示这份镜头表检查有 error、还没装段（本章的段仍是上一次装出的）。
	Draft bool `json:"draft"`
	// Accepted 表示检查有 error 但用户选了先装段：段已装好，问题挂在段和镜头上（Issues），到分镜制作逐张改。
	Accepted bool `json:"accepted"`
	// Issues 是检查的 error 挂到哪几镜、在哪一场、能不能一键修（见 drama.ShotIssues）；侧栏和分镜制作按它标问题、挑要重跑的场。
	Issues []drama.ShotIssue `json:"issues"`
	// CompileProfile 是装段用的编译档案版本（0 为内置默认）。
	CompileProfile DramaCompileProfileRef `json:"compileProfile"`
	// Sections 是按小节生成时每一节的原样输出（技能原生格式，含 BGM 等不进镜头表的内容）、转换统计和转出的镜头；旧镜头表为空。
	Sections  []drama.SectionOutput `json:"sections,omitempty"`
	UpdatedAt time.Time             `json:"updatedAt"`
}

type DramaShotlistWriteResult struct {
	Shotlist DramaShotlistView  `json:"shotlist"`
	Segments []DramaSegmentView `json:"segments"`
	Check    drama.CheckReport  `json:"check"`
	Stats    drama.RowsStats    `json:"stats"`
	// Draft 表示检查有 error，只存了草稿，没有装段（Segments 是本章原有的段）。
	Draft bool `json:"draft"`
	// Accepted 表示检查有 error，按 acceptIssues 带着问题装了段。
	Accepted bool `json:"accepted"`
	// Notes 是装段说明：找不到合法切点的场、渠道时长等，给用户看。
	Notes []string `json:"notes"`
	// Profile 是这章检查、装段用的风格档案及来由（如「真人剧（默认）（真人实拍…）」）。
	Profile string `json:"profile"`
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

// dramaLockCard 把文字设定拆成外貌声明的短句，最多 lockMax 条（编译档案 cast.lock_max，默认 6，与 pack.py 的 cast 取 lock_card 前 6 条一致）。
func dramaLockCard(description string, lockMax int) []string {
	var out []string
	for _, part := range dramaLockSplit.Split(description, -1) {
		if part = strings.TrimSpace(part); part != "" && len(out) < lockMax {
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
// 角色子卡（M8 卡 D）并进主卡那个人物，当成一个变体（标签是变体名、文件是子卡的主版本）：refs_hint 写「名·变体」就取到子卡的图。
func dramaShotlistBible(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary, imaged map[string]bool, lockMax int) *drama.Bible {
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
	character := func(name string) *drama.BibleCharacter {
		for i := range bible.Characters {
			if bible.Characters[i].Name == name {
				return &bible.Characters[i]
			}
		}
		bible.Characters = append(bible.Characters, drama.BibleCharacter{Name: name})
		return &bible.Characters[len(bible.Characters)-1]
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
			if a.Variant != nil && dramaRefRole(a.Category) == "identity" {
				name := firstNonEmpty(a.Variant.ParentTitle, a.Title)
				ch := character(name)
				variant := drama.BibleVariant{Label: a.Variant.Label, Kind: firstNonEmpty(a.Variant.Kind, "state"), Sheet: file, LockCard: dramaLockCard(dramaAssetDescription(a), lockMax)}
				// refs_hint 写过「名·变体」时主卡那边先按主卡的图记了同名变体：换成子卡的。
				if i := slices.IndexFunc(ch.Variants, func(v drama.BibleVariant) bool { return v.Label == variant.Label }); i >= 0 {
					ch.Variants[i] = variant
				} else {
					ch.Variants = append(ch.Variants, variant)
				}
				added[a.Title] = true
				continue
			}
			switch dramaRefRole(a.Category) {
			case "identity":
				lock := dramaLockCard(dramaAssetDescription(a), lockMax)
				ch := character(a.Title)
				ch.Variants = append([]drama.BibleVariant{{Kind: "base", Sheet: file, LockCard: lock}}, ch.Variants...)
				for _, v := range variants[a.Title] {
					if !slices.ContainsFunc(ch.Variants, func(x drama.BibleVariant) bool { return x.Label == v }) {
						ch.Variants = append(ch.Variants, drama.BibleVariant{Label: v, Kind: "state", Sheet: file, LockCard: lock})
					}
				}
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
func sceneOwnLocations(shots []drama.Shot, hints [][]string, locationOf func(string) string, script *drama.Script) map[string]string {
	counts := map[string]map[string]int{}
	order := map[string][]string{}
	for i, sh := range shots {
		for _, h := range hints[i] {
			name := locationOf(h)
			if name == "" {
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
//   - 人物资产只补这一镜真在场的（站位、焦点、分镜行人物里有他），免得同一场景资产串起来的两场戏互相带人。在场按人认：
//     主卡名、这个人任一张子卡的标题、「主卡名·任意」都算同一个人（绑的是主卡、名字写子卡，或反过来，都不丢人）。
//   - 场景只认本场自己的（spec.scene 对应的场景资产，见 sceneOwnLocations）：一镜列了别的场景（段首镜头写到
//     「身后是病房走廊」、同一镜写了两个场景）一律换成本场的，段的场景参考因此只有一个。场景参考是整段生成的背景，
//     两张背景图会让模型在两处之间来回跳；一场里真换了地方，应在剧本里另起一场。本场认不出场景时才留这一镜列的第一个。
func attachRefsHints(shots []drama.Shot, rows []shotlistRow, assets []ProjectAssetSummary, script *drama.Script) {
	type boundAsset struct {
		title     string
		character bool
		// person 是这一镜要在场的人：主卡是自己的名字，子卡是主卡的名字。
		person string
	}
	byID := make(map[string]boundAsset, len(assets))
	locations := map[string]bool{}
	// personOf 是角色卡标题 → 主卡名，人物名先按卡标题认，认不出时取「·」前面的主卡名。
	personOf := map[string]string{}
	for _, a := range assets {
		if a.PrimaryVersionID == "" {
			continue
		}
		title := strings.TrimSpace(a.Title)
		person := title
		if a.Variant != nil && a.Variant.ParentTitle != "" {
			person = a.Variant.ParentTitle
		}
		byID[a.ID] = boundAsset{title, dramaRefRole(a.Category) == "identity", person}
		if dramaRefRole(a.Category) == "identity" {
			personOf[title] = person
		}
		if dramaRefRole(a.Category) == "location" {
			locations[title] = true
		}
	}
	personKey := func(name string) string {
		name = strings.TrimSpace(name)
		if p, ok := personOf[name]; ok {
			return p
		}
		if head, _, found := strings.Cut(name, "·"); found {
			if p, ok := personOf[strings.TrimSpace(head)]; ok {
				return p
			}
		}
		return name
	}
	for i := range shots {
		hints := slices.Clone([]string(shots[i].RefsHint))
		if i < len(rows) {
			named := map[string]bool{}
			for _, h := range hints {
				name, _, _ := strings.Cut(h, "·")
				named[name], named[strings.TrimSpace(h)] = true, true
			}
			present := map[string]bool{personKey(shots[i].Focus): true}
			for _, b := range shots[i].Blocking {
				present[personKey(b.Who)] = true
			}
			for _, c := range rows[i].Characters {
				present[personKey(c.CharacterName)] = true
			}
			for _, id := range rows[i].assetIDs() {
				a := byID[id]
				if a.title == "" || named[a.title] || (a.character && !present[a.person]) {
					continue
				}
				named[a.title] = true
				hints = append(hints, a.title)
			}
		}
		shots[i].RefsHint = hints
	}
	keepSceneLocations(shots, locations, script)
}

// keepSceneLocations 把每镜 refs_hint 里的场景收成本场自己的一个（见 attachRefsHints）。locations 是有主版本的场景资产名；
// 模型写的「电梯口（夜）」这类对不上资产名的提示原样留着（装段找不到它，不成参考）。
func keepSceneLocations(shots []drama.Shot, locations map[string]bool, script *drama.Script) {
	locationOf := func(hint string) string {
		if name, _, _ := strings.Cut(hint, "·"); locations[name] {
			return name
		}
		return ""
	}
	all := make([][]string, len(shots))
	for i := range shots {
		all[i] = shots[i].RefsHint
	}
	own := sceneOwnLocations(shots, all, locationOf, script)
	for i := range shots {
		keep := own[shots[i].Scene]
		hints := all[i][:0:0]
		placed := false
		for _, h := range all[i] {
			name := locationOf(h)
			switch {
			case name == "":
				hints = append(hints, h)
			case placed:
			case keep == "" || name == keep:
				// 本场认不出场景时留这一镜列的第一个；本场的场景原样保留（带变体）。
				hints, placed = append(hints, h), true
			default:
				hints, placed = append(hints, keep), true
			}
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

// dramaCheckContext 是检查用的剧本、资产圣经（场景锚点）、风格档案（按项目画风对照）、词库与规则级别。
func dramaCheckContext(script *drama.Script, pc dramaProjectContext, assets []ProjectAssetSummary) (drama.CheckContext, *drama.Profile, error) {
	profile, err := drama.BuiltinProfile(pc.profileID())
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
	checkScript, bible := dramaCheckScene(script, assets)
	return drama.CheckContext{Script: checkScript, Bible: bible, Profile: profile, Vocab: vocab, Rules: rules}, profile, nil
}

// dramaShotlistScript 是写镜头表用的台词表：正文切不出场时为 nil。legacy 为 true 时按旧规则切（重装、评测规则改变前写的镜头表）。
func dramaShotlistScript(unit *model.ProjectUnit, legacy bool) *drama.Script {
	text := chapterPlainText(unit.SourceText)
	parsed := drama.ScriptFromText(text, unit.Title)
	if legacy {
		parsed = drama.LegacyScriptFromText(text, unit.Title)
	}
	if len(parsed.Scenes) == 0 {
		return nil
	}
	return &parsed
}

// legacyScenePalettes 把按新场编号的色卡换成按旧场编号：旧第 k 场用它现在所在那一场的色卡（同一地点）。
func legacyScenePalettes(unit *model.ProjectUnit, palettes map[string]string) map[string]string {
	if len(palettes) == 0 {
		return palettes
	}
	out := map[string]string{}
	for _, sc := range drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title) {
		if v := palettes[sc.ID]; v != "" {
			for _, no := range sc.LegacyNos {
				out[drama.SceneCode(1, no)] = v
			}
		}
	}
	return out
}

// storedScriptLegacy 是存下的台词表是切场规则改变前（一个场次头一场）切的。
func storedScriptLegacy(scriptJSON string) bool {
	var stored drama.Script
	return strings.TrimSpace(scriptJSON) != "" && json.Unmarshal([]byte(scriptJSON), &stored) == nil && stored.SceneRule == 0
}

// WriteDramaShotlist 把一章的分镜行转成镜头表、检查、装段并整章替换段：检查有 error 时整批拒绝，不落库
// （KeepDraft 时改为只存草稿，见 DramaShotlistWriteRequest）。
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
	if err := shotlistRowsWithinCap(req.Rows); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	rows, err := parseShotlistRows(req.Rows)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	m, err := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, req.Rule))
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	script := dramaShotlistScript(unit, req.LegacyScenes)
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	phrases, err := s.activeCompileProfile()
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	imaged, err := s.dramaImagedAssets(assets)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	pc := projectDramaContext(project)
	pc.Episode = s.dramaChapterEpisode(projectID, unit)
	if pc.SceneCast, err = s.unitSceneCast(project, unit.ID, assets); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	plan, err := planDramaShotlistWith(rows, script, assets, imaged, m, phrases, pc, req.AcceptIssues)
	plan.Report.Parse = req.Parse
	sections, err2 := s.dramaShotlistSections(projectID, unitID, req.Sections, plan.Shotlist)
	if err2 != nil {
		return DramaShotlistWriteResult{}, err2
	}
	var appErr *AppError
	if err != nil && req.KeepDraft && errors.As(err, &appErr) && appErr.Reason == ReasonShotlistCheckFailed {
		return s.writeDramaShotlistDraft(userID, projectID, unitID, req, plan, script, phrases, pc, sections)
	}
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	if req.AcceptIssues && plan.Report.HasErrors() {
		if err := acceptedPackBlocked(plan, m); err != nil {
			return DramaShotlistWriteResult{}, err
		}
		plan.Report.Accepted = true
	}
	shotlist, report, segments, problems := plan.Shotlist, plan.Report, plan.Segments, plan.Problems
	rowByShot := make(map[string]shotlistRow, len(rows))
	for i, sh := range shotlist.Shots {
		rowByShot[sh.ID] = rows[i]
	}
	palettes, err := s.unitScenePalettes(userID, project, unitID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	if req.LegacyScenes {
		palettes = legacyScenePalettes(unit, palettes)
	}
	inputs := make([]ReplaceProjectUnitShotInput, 0, len(segments))
	for _, seg := range segments {
		input, err := dramaSegmentShotInput(seg, rowByShot)
		if err != nil {
			return DramaShotlistWriteResult{}, err
		}
		input.StyleVersionIDs = segmentPaletteVersions(seg, palettes)
		inputs = append(inputs, input)
	}
	// 5 镜头表与段同一事务写入
	shotlistJSON, _ := json.Marshal(shotlist)
	checkJSON, _ := json.Marshal(report)
	rowsJSON, _ := json.Marshal(req.Rows)
	record := &model.DramaShotlist{ShotlistJSON: string(shotlistJSON), CheckJSON: string(checkJSON), RowsJSON: string(rowsJSON), SectionsJSON: sectionsJSON(sections), CompileProfileVersion: phrases.Version()}
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
	if report.Accepted {
		notes = append(notes, fmt.Sprintf("镜头表检查有 %d 个问题，已带着问题装段；问题标在分镜制作的段和镜头上，逐张改完会自动重检", report.Errors))
	}
	view := DramaShotlistView{UnitID: unitID, Version: record.Version, Shotlist: shotlist, Script: script, Check: report, Accepted: report.Accepted, Issues: drama.ShotIssues(report, shotlist, script, "error"), CompileProfile: compileProfileRef(phrases), Sections: sections, UpdatedAt: record.UpdatedAt}
	return DramaShotlistWriteResult{Shotlist: view, Segments: views, Check: report, Stats: plan.Stats, Accepted: report.Accepted, Notes: notes, Profile: dramaProfileNote(pc)}, nil
}

// acceptedPackBlocked 是带问题装段时仍要拒绝的情形：这些问题让装出来的段本身不成立，改分镜卡也救不回来。
//   - 镜头时长（spec.est）不是正数：段内时间轴排不出来。
//   - 一场找不到合法切点（接话不拆、每段在上下限内），整场装成一段又超过单段上限：段会被压到上限、镜头挤在一起。
func acceptedPackBlocked(plan dramaShotlistPlan, m drama.Model) error {
	var reasons []string
	est := make(map[string]float64, len(plan.Shotlist.Shots))
	for _, sh := range plan.Shotlist.Shots {
		// 镜头表转换时没写时长的镜头已补成分镜行时长（或 3 秒），这里剩下的是写成了 0 以下的。
		if sh.Est <= 0 {
			reasons = append(reasons, fmt.Sprintf("镜头 %s 的时长是 %v 秒", sh.ID, float64(sh.Est)))
		}
		est[sh.ID] = float64(sh.Est)
	}
	for _, seg := range plan.Segments {
		content := 0.0
		for _, id := range seg.ShotIDs {
			content += est[id]
		}
		switch {
		case content <= float64(m.Duration.Max):
		case seg.Section != "":
			reasons = append(reasons, fmt.Sprintf("小节 %s 镜头合计 %.1f 秒，超过 %s 一段的上限 %d 秒（一节装一段，不在节内切）：先把这一节拆成两节重新生成", seg.Section, content, m.Label, m.Duration.Max))
		default:
			reasons = append(reasons, fmt.Sprintf("%s（%s）镜头合计 %.1f 秒，超过 %s 一段的上限 %d 秒，又找不到合法切点", seg.ID, seg.Scene, content, m.Label, m.Duration.Max))
		}
	}
	if len(reasons) == 0 {
		return nil
	}
	err := NewAppError(http.StatusBadRequest, "这些问题会让装段本身做不成，不能带着问题装段，请重新生成或改正文后再装："+strings.Join(reasons, "；"))
	err.Reason = ReasonShotlistCheckFailed
	err.Details = map[string]any{"check": plan.Report, "stats": plan.Stats, "blocked": reasons}
	return err
}

// writeDramaShotlistDraft 存检查没过的镜头表：分镜行、镜头表、台词表、检查结果写进镜头表记录，本章的段不动。
func (s *Service) writeDramaShotlistDraft(userID string, projectID string, unitID string, req DramaShotlistWriteRequest, plan dramaShotlistPlan, script *drama.Script, phrases *drama.CompileProfile, pc dramaProjectContext, sections []drama.SectionOutput) (DramaShotlistWriteResult, error) {
	shotlistJSON, _ := json.Marshal(plan.Shotlist)
	checkJSON, _ := json.Marshal(plan.Report)
	rowsJSON, _ := json.Marshal(req.Rows)
	now := time.Now()
	record := &model.DramaShotlist{ID: newID(), ProjectID: projectID, UnitID: unitID, ShotlistJSON: string(shotlistJSON), CheckJSON: string(checkJSON), RowsJSON: string(rowsJSON),
		SectionsJSON: sectionsJSON(sections), CompileProfileVersion: phrases.Version(), CreatedBy: userID, CreatedAt: now, UpdatedAt: now}
	if script != nil {
		scriptJSON, _ := json.Marshal(script)
		record.ScriptJSON = string(scriptJSON)
	}
	if err := s.repo.SaveDramaShotlist(record); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	notes := []string{fmt.Sprintf("镜头表检查有 %d 个 error，已存为草稿，没有装段；处理后重新装段", plan.Report.Errors)}
	view := DramaShotlistView{UnitID: unitID, Version: record.Version, Shotlist: plan.Shotlist, Script: script, Check: plan.Report, Draft: true, Issues: drama.ShotIssues(plan.Report, plan.Shotlist, script, "error"), CompileProfile: compileProfileRef(phrases), Sections: sections, UpdatedAt: record.UpdatedAt}
	return DramaShotlistWriteResult{Shotlist: view, Segments: views, Check: plan.Report, Stats: plan.Stats, Draft: true, Notes: notes, Profile: dramaProfileNote(pc)}, nil
}

// dramaShotlistSections 是这次写镜头表要存的小节原样输出：给了就用，没给时沿用本章上一版存下的；每一节转出的镜头按这次的镜头表重填。
// 镜头表已经不按小节（镜头都没写 section）时返回 nil，sections_json 清空。
func (s *Service) dramaShotlistSections(projectID string, unitID string, given []drama.SectionOutput, shotlist drama.Shotlist) ([]drama.SectionOutput, error) {
	if !slices.ContainsFunc(shotlist.Shots, func(sh drama.Shot) bool { return sh.Section != "" }) {
		return nil, nil
	}
	sections := given
	if sections == nil {
		previous, err := s.repo.DramaShotlistForUnit(projectID, unitID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(previous.SectionsJSON) == "" || json.Unmarshal([]byte(previous.SectionsJSON), &sections) != nil {
			return nil, nil
		}
	}
	out := slices.Clone(sections)
	for i := range out {
		out[i].Shots = []string{}
		for _, sh := range shotlist.Shots {
			if sh.Section == out[i].ID {
				out[i].Shots = append(out[i].Shots, sh.ID)
			}
		}
	}
	return out, nil
}

func sectionsJSON(sections []drama.SectionOutput) string {
	if len(sections) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(sections)
	return string(encoded)
}

// briefSegmentRule 把需求文档的单段秒数（brief_json.segment_seconds，建项目时选的 15 / 30 秒）叠到段长规则上：
// 取两者里更严的最长秒数；最终仍由 dramaShotlistModel 夹在项目生成模型的上限内（只收紧不放宽）。
func briefSegmentRule(briefJSON string, rule *DramaShotlistRule) *DramaShotlistRule {
	doc := map[string]any{}
	if raw := strings.TrimSpace(briefJSON); raw != "" {
		_ = json.Unmarshal([]byte(raw), &doc)
	}
	seconds, _ := doc["segment_seconds"].(float64)
	if seconds < 1 {
		return rule
	}
	out := DramaShotlistRule{}
	if rule != nil {
		out = *rule
	}
	if out.MaxSeconds <= 0 || int(seconds) < out.MaxSeconds {
		out.MaxSeconds = int(seconds)
	}
	return &out
}

// chapterShotsOverCap 是建按小节生成任务前的预估：按最密的一秒一镜估，各节估时合计超过一章镜头上限就不建任务，
// 免得模型把各节都写完了、收尾才因镜头太多被拒。估不准的情形由收尾的 shotlistRowsWithinCap 兜底（各节输出留在任务里，可重新收尾）。
func chapterShotsOverCap(shots *dramaShotsMeta) error {
	if shots == nil {
		return nil
	}
	seconds := 0.0
	for _, sec := range shots.Sections {
		seconds += sec.Est
	}
	if seconds <= float64(dramaChapterMaxShots) {
		return nil
	}
	return BadAuthRequest(fmt.Sprintf("本章切成 %d 节、估计约 %.0f 分钟，按一秒一镜可能超过一章 %d 个镜头的上限：请在剧本里把这一章拆成两章（或两集）再生成分段", len(shots.Sections), seconds/60, dramaChapterMaxShots))
}

// shotlistRowsWithinCap 是写镜头表前的数量和体积检查；超了说清本章多少镜、上限多少、怎么办。
func shotlistRowsWithinCap(raws []json.RawMessage) error {
	if len(raws) == 0 {
		return BadAuthRequest("没有分镜行，镜头表没有写入")
	}
	if len(raws) > dramaChapterMaxShots {
		return shotlistTooLarge(fmt.Sprintf("本章 %d 个镜头超过一章 %d 个镜头的上限，镜头表没有写入：请在剧本里把这一章拆成两章（或两集）再生成分段", len(raws), dramaChapterMaxShots))
	}
	total := 0
	for _, raw := range raws {
		total += len(raw)
	}
	if total > dramaShotlistMaxBytes {
		return shotlistTooLarge(fmt.Sprintf("本章分镜行合计 %d MB，超过一次写入 %d MB 的上限，镜头表没有写入：请把这一章拆成两章再写", total>>20, dramaShotlistMaxBytes>>20))
	}
	return nil
}

// shotlistTooLarge 是超过整章上限的 400：reason 让前端知道「重新写入」也会再失败（生成分段任务的 errorCode 同它）。
func shotlistTooLarge(message string) error {
	err := NewAppError(http.StatusBadRequest, message)
	err.Reason = ReasonShotlistTooLarge
	return err
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
	// Lines 是剧本台词的逐字覆盖（评测门槛用）。
	Lines    drama.LineCoverage
	Segments []drama.Segment
	Problems []string
}

// planDramaShotlist 是写镜头表不碰数据库的部分：分镜行 → 镜头表 → 检查（有 error 返回 shotlist_check_failed）→ 按场装段。
// 场按镜头表的 scene（E01S01…）分，不按绑定的场景资产分：两场戏用同一个场景资产时各装各的段，参考取各自镜头。
// 风格档案按项目画风对照（pc），分段合计按每集目标时长查 R-SEG-008（warn，不拦保存）。
func planDramaShotlist(rows []shotlistRow, script *drama.Script, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model, phrases *drama.CompileProfile, pc dramaProjectContext) (dramaShotlistPlan, error) {
	return planDramaShotlistWith(rows, script, assets, imaged, m, phrases, pc, false)
}

// planDramaShotlistWith 同 planDramaShotlist；accept 为 true 时检查有 error 也照样装段（带问题装段、分镜卡编辑后重编）。
func planDramaShotlistWith(rows []shotlistRow, script *drama.Script, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model, phrases *drama.CompileProfile, pc dramaProjectContext, accept bool) (dramaShotlistPlan, error) {
	if phrases == nil {
		var err error
		if phrases, err = drama.DefaultCompileProfile(); err != nil {
			return dramaShotlistPlan{}, err
		}
	}
	storyRows := make([]drama.StoryboardRow, 0, len(rows))
	for _, row := range rows {
		storyRows = append(storyRows, row.StoryboardRow)
	}
	converted := drama.RowsToShotlist(storyRows, script, pc.profileID())
	// 这一场的镜头绑本场的场景资产、角色换成本场的子卡（rows 原地改，写段时的资产引用跟着换）。
	applySceneCast(rows, converted.Shotlist.Shots, pc.SceneCast)
	plan := dramaShotlistPlan{Shotlist: converted.Shotlist, Stats: converted.Stats, Lines: converted.Lines}
	ctx, profile, err := dramaCheckContext(script, pc, assets)
	if err != nil {
		return plan, err
	}
	// 按小节生成的镜头表：技能不产出的表演字段和档案配额按戏类放宽成 warn；一节估时超过单段上限是 error（一节一段，不在节内切）。
	ctx.WarnOnly = drama.GenreRelaxedRules(drama.ShotlistGenre(plan.Shotlist.Shots))
	plan.Report = drama.CheckShots(plan.Shotlist, ctx)
	plan.Report.AddSchemaIssues(converted.SchemaIssues)
	segmentRules, err := drama.BuiltinSegmentRules()
	if err != nil {
		return plan, err
	}
	plan.Report.CheckSectionDurations(plan.Shotlist, script, m, segmentRules)
	if plan.Report.HasErrors() && !accept {
		return plan, shotlistCheckFailed(plan.Report, converted.Stats)
	}
	plan.Segments, plan.Problems = packDramaShotlist(plan.Shotlist, rows, script, assets, imaged, m, profile, ctx.Vocab, phrases)
	if err := checkDramaSegmentTotal(&plan.Report, plan.Segments, pc); err != nil {
		return plan, err
	}
	return plan, nil
}

// checkDramaSegmentTotal 把 R-SEG-008（分段合计与每集目标片长比）记进镜头表检查结果。
func checkDramaSegmentTotal(report *drama.CheckReport, segments []drama.Segment, pc dramaProjectContext) error {
	rules, err := drama.BuiltinSegmentRules()
	if err != nil {
		return err
	}
	report.CheckSegmentTotal(segments, pc.targetSeconds(), rules)
	return nil
}

// dramaProfileNote 说明这章按哪份风格档案检查、装段，从哪来。
func dramaProfileNote(pc dramaProjectContext) string {
	label := pc.profileID()
	if p, err := drama.BuiltinProfile(label); err == nil && p.Label != "" {
		label = p.Label
	}
	switch {
	case pc.ProfileRule != "":
		return label + "（" + pc.ProfileRule + "）"
	case pc.HasStyle:
		return label + "（项目画风没有对上对照表，用默认档案）"
	default:
		return label + "（项目没选画风，用默认档案）"
	}
}

// packDramaShotlist 是检查通过后的装段：镜头挂上分镜行绑定的资产（rows 与镜头一一对应），拼资产圣经，按场装段，没图的场景、道具写进镜头描述。
func packDramaShotlist(shotlist drama.Shotlist, rows []shotlistRow, script *drama.Script, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model, profile *drama.Profile, vocab *drama.Vocab, phrases *drama.CompileProfile) ([]drama.Segment, []string) {
	packShots := slices.Clone(shotlist.Shots)
	attachRefsHints(packShots, rows, assets, script)
	return packHintedShots(shotlist, packShots, rows, script, assets, imaged, m, profile, vocab, phrases)
}

// packHintedShots 是装段的后半：镜头 refs_hint 已经挂好资产名，rows 只用来决定哪些资产进圣经。
func packHintedShots(shotlist drama.Shotlist, packShots []drama.Shot, rows []shotlistRow, script *drama.Script, assets []ProjectAssetSummary, imaged map[string]bool, m drama.Model, profile *drama.Profile, vocab *drama.Vocab, phrases *drama.CompileProfile) ([]drama.Segment, []string) {
	bible := dramaShotlistBible(packShots, rows, assets, imaged, phrases.Cast.LockMax)
	segments, problems := drama.Pack(drama.PackInput{
		Shotlist: drama.Shotlist{Profile: shotlist.Profile, Episode: shotlist.Episode, Shots: packShots},
		Script:   script, Bible: bible, Model: m, Profile: profile, ProfileID: shotlist.Profile, Vocab: vocab,
		FileExists: func(string) bool { return true }, Phrases: phrases,
	})
	describeTextOnlyAssets(segments, packShots, assets, imaged)
	return segments, problems
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

// DramaShotlistRepackRequest 是用已存镜头表重新装段的参数：rule 与写镜头表时一样，是渠道实际能提交的段长。
// 镜头表是检查没过的草稿时，重装仍要过检查：还有 error 就和写镜头表一样报 shotlist_check_failed，草稿不变；
// acceptIssues 为 true 时是「先装段，到分镜制作再改」：带着 error 装段（见 DramaShotlistWriteRequest.AcceptIssues）。
type DramaShotlistRepackRequest struct {
	Rule         *DramaShotlistRule `json:"rule,omitempty"`
	AcceptIssues bool               `json:"acceptIssues,omitempty"`
}

// RepackDramaShotlist 用这一章已存的分镜行原文按当前生效的编译档案重新检查、装段（不调模型）：改了镜头画面句类的文字后用它让段换上新写法。
// 与写镜头表一样整章替换段，旧段的候选视频和证据一并删除。
func (s *Service) RepackDramaShotlist(userID string, projectID string, unitID string, req DramaShotlistRepackRequest) (DramaShotlistWriteResult, error) {
	if _, err := s.activeProjectForUser(userID, projectID); err != nil {
		return DramaShotlistWriteResult{}, err
	}
	unitID = strings.TrimSpace(unitID)
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return DramaShotlistWriteResult{}, BadAuthRequest("这一章还没有镜头表，先生成分段")
	}
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	if strings.TrimSpace(record.RowsJSON) == "" {
		return DramaShotlistWriteResult{}, BadAuthRequest("这一章的镜头表保存时还没有记录分镜行原文，不能直接重装；请重新生成分段")
	}
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(record.RowsJSON), &rows); err != nil {
		return DramaShotlistWriteResult{}, fmt.Errorf("镜头表分镜行原文损坏: %w", err)
	}
	views, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return DramaShotlistWriteResult{}, err
	}
	expected := make([]string, 0, len(views))
	for _, v := range views {
		expected = append(expected, v.ShotID)
	}
	// 解析统计只在生成分段收尾时有，重装沿用上一次的。
	var previous drama.CheckReport
	_ = json.Unmarshal([]byte(firstNonEmpty(record.CheckJSON, "{}")), &previous)
	// 切场规则改变前写的镜头表（正文有并起来的场）：分镜行里的场编号是旧的，重装仍按旧规则切台词表，不换编号；要换成新场次就重新生成分段。
	legacy := false
	if storedScriptLegacy(record.ScriptJSON) {
		unit, err := s.repo.ProjectUnit(projectID, unitID)
		if err != nil {
			return DramaShotlistWriteResult{}, err
		}
		legacy = drama.ScenesRenumbered(drama.SplitScenes(chapterPlainText(unit.SourceText), unit.Title))
	}
	// 已经带着问题装过段的，重装（改了时长、资产、编译档案）照样带着问题装，不用再选一次。
	accept := req.AcceptIssues || previous.Accepted
	return s.WriteDramaShotlist(userID, projectID, unitID, DramaShotlistWriteRequest{Rows: rows, Rule: req.Rule, ExpectedShotIDs: expected, Parse: previous.Parse, LegacyScenes: legacy, AcceptIssues: accept})
}

// DramaShotlist 读一章的镜头表、台词表与最近一次检查结果；这一章还没有镜头表时返回 nil。
func (s *Service) DramaShotlist(userID string, projectID string, unitID string) (*DramaShotlistView, error) {
	if _, err := s.repo.ProjectForUser(userID, projectID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("项目不存在")
		}
		return nil, err
	}
	unitID = strings.TrimSpace(unitID)
	if _, err := s.repo.ProjectUnit(projectID, unitID); err != nil {
		// 单元删了（如剧本优化替换掉的旧剧集）时页面可能还在读：返回 404，不是 500。
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, NotFound("章节或剧集不存在")
		}
		return nil, err
	}
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	view := &DramaShotlistView{UnitID: unitID, Version: record.Version, CompileProfile: DramaCompileProfileRef{Version: record.CompileProfileVersion, Name: "内置默认"}, UpdatedAt: record.UpdatedAt}
	if record.CompileProfileVersion > 0 {
		view.CompileProfile.Name = fmt.Sprintf("v%d", record.CompileProfileVersion)
		if p, err := s.repo.DramaCompileProfileByVersion(record.CompileProfileVersion); err == nil {
			view.CompileProfile.Name = p.Name
		}
	}
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
	view.Accepted = view.Check.HasErrors() && view.Check.Accepted
	view.Draft = view.Check.HasErrors() && !view.Check.Accepted
	view.Issues = drama.ShotIssues(view.Check, view.Shotlist, view.Script, "error")
	if strings.TrimSpace(record.SectionsJSON) != "" {
		_ = json.Unmarshal([]byte(record.SectionsJSON), &view.Sections)
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
func dropImagelessRefs(seg drama.Segment, lookup func(file string) dramaRefAsset, lockMax int) (drama.Segment, []drama.Issue) {
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
			lock := dramaLockCard(dramaAssetDescription(info.asset), lockMax)
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
