package drama

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// 镜头表门禁（与 check.py 的 check_shots 一致）：R-SHOT-001～030，级别取规则表 rules/shots.json。
// 读不到剧本时跳过覆盖率（R-SHOT-001）；没有资产圣经时依赖空间图、锚点、伏笔的规则自然不触发。

// Bible 是资产圣经里装段、检查会读的部分（contracts/bible.schema.json）。
type Bible struct {
	Characters  []BibleCharacter `json:"characters"`
	Locations   []BibleLocation  `json:"locations"`
	Props       []BibleProp      `json:"props"`
	SpatialMaps []SpatialMap     `json:"spatial_maps"`
	Plants      []BiblePlant     `json:"plants"`
	// StyleAnchor / ColorScript 是导演风格写的全局风格锚和逐场色彩脚本（装段、检查暂不读）。风格锁定（M8）也写 StyleAnchor。
	StyleAnchor *StyleAnchor       `json:"style_anchor,omitempty"`
	ColorScript []ColorScriptEntry `json:"color_script,omitempty"`
	// MasterPalette / StyleLock 是项目锁定的主基调色卡和锁定来源（风格锁定或平台风格库写，装段、检查暂不读）。
	MasterPalette *MasterPalette   `json:"master_palette,omitempty"`
	StyleLock     *StyleLockRecord `json:"style_lock,omitempty"`
}

type BibleVariant struct {
	Label    string   `json:"label"`
	Kind     string   `json:"kind"`
	Sheet    string   `json:"sheet"`
	Voice    string   `json:"voice"`
	Crops    []string `json:"crops"`
	LockCard []string `json:"lock_card"`
}

type BibleCharacter struct {
	Name         string         `json:"name"`
	Speaks       bool           `json:"speaks"`
	Anchors      []string       `json:"anchors"`
	Variants     []BibleVariant `json:"variants"`
	VoiceProfile struct {
		Timbre      string `json:"timbre"`
		LineQuality string `json:"line_quality"`
	} `json:"voice_profile"`
	GroupVariation string `json:"group_variation"`
}

type BibleLocation struct {
	Name        string   `json:"name"`
	SpaceType   string   `json:"space_type"`
	Anchors     []string `json:"anchors"`
	LightSource string   `json:"light_source"`
	Plate       string   `json:"plate"`
	SideA       string   `json:"side_a"`
	SideB       string   `json:"side_b"`
	Versions    []struct {
		Label string `json:"label"`
		Plate string `json:"plate"`
	} `json:"versions"`
}

type BibleProp struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	States []struct {
		When  string `json:"when"`
		Image string `json:"image"`
	} `json:"states"`
}

type SpatialMap struct {
	Scene     string `json:"scene"`
	Positions []struct {
		Who string `json:"who"`
		At  string `json:"at"`
	} `json:"positions"`
}

type BiblePlant struct {
	ID    string `json:"id"`
	What  string `json:"what"`
	Scene string `json:"scene"`
}

func (b *Bible) location(name string) *BibleLocation {
	if b == nil || name == "" {
		return nil
	}
	for i := range b.Locations {
		if b.Locations[i].Name == name {
			return &b.Locations[i]
		}
	}
	return nil
}

// CheckItem 是一条门禁结果；ShotID 是规则指向的镜头（整表级规则为空）。
type CheckItem struct {
	Level   string `json:"level"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
	ShotID  string `json:"shotId,omitempty"`
}

// CheckSkip 是因为缺输入而跳过的规则。
type CheckSkip struct {
	Rule string `json:"rule"`
	Why  string `json:"why"`
}

// CheckReport 是一次检查的结果（check.py Report.to_dict）。
type CheckReport struct {
	Kind     string      `json:"kind"`
	Errors   int         `json:"errors"`
	Warnings int         `json:"warnings"`
	Items    []CheckItem `json:"items"`
	Skipped  []CheckSkip `json:"skipped"`
	// Parse 是生成分段收尾时模型输出的解析统计（服务端解析才有；手写镜头表、Agent 写入时为空）。
	Parse *ShotLinesStats `json:"parse,omitempty"`
}

func (r *CheckReport) add(rules RuleLevels, rule, shotID, level, message string) {
	if level == "" {
		level = rules[rule]
		if level == "" {
			level = "warn"
		}
	}
	r.Items = append(r.Items, CheckItem{Level: level, Rule: rule, Message: message, ShotID: shotID})
	if level == "error" {
		r.Errors++
	} else {
		r.Warnings++
	}
}

// HasErrors 有没有 error 级问题。
func (r CheckReport) HasErrors() bool { return r.Errors > 0 }

// CheckContext 是检查镜头表用到的剧本、资产圣经、档案、词库与规则级别。Script / Bible 可为空。
type CheckContext struct {
	Script  *Script
	Bible   *Bible
	Profile *Profile
	Vocab   *Vocab
	Rules   RuleLevels
}

var (
	shotSizes   = []string{"大远景", "远景", "全景", "中全景", "中景", "中近景", "近景", "特写", "大特写"}
	zOfSize     = map[string]string{"大远景": "Z9", "远景": "Z8", "全景": "Z7", "中全景": "Z6", "中景": "Z5", "中近景": "Z4", "近景": "Z3", "特写": "Z2", "大特写": "Z1"}
	wideSizes   = map[string]bool{"大远景": true, "远景": true, "全景": true, "中全景": true, "中景": true}
	closeSizes  = map[string]bool{"特写": true, "大特写": true}
	actionVerbs = []string{"走", "转", "起", "坐", "递", "拿", "抬", "放", "推", "拉", "倒", "摸", "敲", "点", "摔", "靠", "退", "上前", "低头", "抬头", "伸手", "握", "松", "翻", "撑", "靠近", "后退", "整理", "踱", "跑", "跳", "甩", "挥", "踢", "打", "拔", "收", "扶", "蹲", "站", "躺", "扑", "拽", "捶", "拍", "指", "擦", "撩", "挠", "搓", "掀", "开", "关", "系", "解", "穿", "脱", "喝", "吃", "写", "按", "滑", "撞", "挪", "插", "攥", "捏", "扣", "叠", "揣", "绕", "掏", "塞", "拭", "抹", "揉", "搂", "托", "举", "蹭", "挡", "探", "缩", "蜷", "撑住", "拨"}
	moveAxis    = map[string]string{"push": "z", "pull": "z", "zoom": "z", "dolly_zoom": "z", "crane_up": "y", "crane_down": "y", "truck": "x", "track": "x", "pan": "rot", "tilt": "rot", "roll": "rot", "orbit": "x", "spiral": "xy", "handheld_micro": "", "handheld": "", "handheld_violent": "", "rack_focus": "", "fixed": "", "snorricam": "", "jib": "xy", "fpv": "xy", "one_take": ""}
	bgmWords    = []string{"配乐", "BGM", "背景音乐", "弦乐", "钢琴声", "鼓点", "音乐卡点", "紧张弦乐", "温暖钢琴", "史诗鼓点"}
	genericEmo  = []string{"惊讶", "悲伤", "愤怒", "紧张", "害怕", "震惊", "开心", "难过", "焦虑"}
	// 小墨第 9 条：空间类型的通用默认禁令，档案 space_policy 可覆盖。
	defaultSpacePolicy = map[string]SpacePolicy{"S1": {MaxSize: "中全景"}, "S2": {MaxSize: "全景"}, "S3": {MaxSize: "远景"}, "S4": {}, "O1": {}, "O2": {}, "X": {MaxSize: "中全景"}}

	screenTextWords   = regexp.MustCompile(`招牌|字幕|写着|纸条上|屏幕上的字|文字`)
	stateChangeAction = regexp.MustCompile(`(从|由).{0,12}(变|改|起身|转身|走到|移到)`)
	noFillPattern     = regexp.MustCompile(`(无|没有|不|不加|不用)(额外)?(补光|反光板|柔光箱|灯板)`)
	fillWords         = []string{"补光", "反光板", "柔光箱", "霓虹补", "LED 板", "LED板", "灯板"}
)

func sizeIndex(size string) int { return slices.Index(shotSizes, size) }

func hasActionVerb(text string) bool {
	for _, v := range actionVerbs {
		if strings.Contains(text, v) {
			return true
		}
	}
	return false
}

// EstSeconds 估一个镜头的时长（check.est_seconds）：写了 est 用它；否则台词按字速 + 动作最短时长 + 表情读秒。
func EstSeconds(sh *Shot, lines scriptLines) float64 {
	if sh.Est != 0 {
		return float64(sh.Est)
	}
	t := 0.0
	for _, ln := range sh.Lines {
		beat, ok := lines.byID[ln.LineID]
		text := ""
		if ok {
			text = beat.Text
		}
		rate := 4.5
		switch {
		case containsAny(ln.Delivery, "喊", "吼", "急", "命令"):
			rate = 6.0
		case containsAny(ln.Delivery, "哽咽", "犹豫", "撒娇", "告白", "哭"):
			rate = 3.0
		case ok && beat.Kind == "vo":
			rate = 3.5
		}
		n := float64(runeLen(stripPunct(text)))
		t += math.Max(0.8, n/rate) + 0.3
	}
	verbs := 0
	for _, v := range actionVerbs {
		if strings.Contains(sh.Action, v) {
			verbs++
		}
	}
	t += 1.0 + float64(min(verbs, 4))*0.6
	if len(sh.Performance.beats()) > 0 {
		t += 0.5
	}
	if sh.Fight.truthy() {
		t = math.Max(t, 1.5)
	}
	return pyRound(math.Max(1.0, t), 1)
}

func containsAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

func isNormalShot(sh *Shot) bool {
	f := sh.camF()
	if !(sh.Cam.Y == "Y3" || sh.Cam.Y == "Y4" || sh.Cam.Y == "Y5") || sh.Cam.X != "X1" || f.Geometry == "dutch" {
		return false
	}
	allowed := map[string]bool{"fixed": true, "push": true, "pull": true, "truck": true, "pan": true, "tilt": true, "track": true}
	for _, m := range sh.Move {
		if !allowed[m] {
			return false
		}
	}
	return true
}

func sameMoves(a, b []string) bool {
	x, y := slices.Clone(a), slices.Clone(b)
	sort.Strings(x)
	sort.Strings(y)
	return slices.Equal(x, y)
}

func (c CheckContext) vfxIDs() map[string]bool {
	ids := map[string]bool{}
	if c.Vocab == nil {
		return ids
	}
	for _, v := range c.Vocab.Vfx {
		ids[v.ID] = true
	}
	return ids
}

// CheckShots 检查一张镜头表。
func CheckShots(shotlist Shotlist, ctx CheckContext) CheckReport {
	R := CheckReport{Kind: "shots", Items: []CheckItem{}, Skipped: []CheckSkip{}}
	add := func(rule, shotID, message string) { R.add(ctx.Rules, rule, shotID, "", message) }
	addLevel := func(rule, level, message string) { R.add(ctx.Rules, rule, "", level, message) }
	prof := ctx.Profile
	if prof == nil {
		prof = &Profile{}
	}
	rulesP, quotas := prof.Rules, prof.Quotas
	script, bible := ctx.Script, ctx.Bible
	if bible == nil {
		bible = &Bible{}
	}
	shots := shotlist.Shots
	lines := newScriptLines(script)
	// 镜头表写了 episode 时只对那一集的台词与伏笔算覆盖（多集项目一集一张镜头表）。
	epPrefix := ""
	if shotlist.Episode != nil {
		epPrefix = fmt.Sprintf("E%02d", *shotlist.Episode)
		filtered := scriptLines{byID: lines.byID}
		for _, id := range lines.order {
			if strings.HasPrefix(id, epPrefix) {
				filtered.order = append(filtered.order, id)
			}
		}
		lines = filtered
	}
	inEpisode := map[string]bool{}
	for _, id := range lines.order {
		inEpisode[id] = true
	}
	// 覆盖
	if script != nil {
		var usedOrder []string
		used := map[string][]string{}
		for _, sh := range shots {
			for _, ln := range sh.Lines {
				if _, ok := used[ln.LineID]; !ok {
					usedOrder = append(usedOrder, ln.LineID)
				}
				used[ln.LineID] = append(used[ln.LineID], sh.ID)
			}
		}
		for _, lid := range lines.order {
			where := used[lid]
			if len(where) == 0 {
				add("R-SHOT-001", "", fmt.Sprintf("台词 %s 没有落在任何镜头：「%s」", lid, runePrefix(lines.byID[lid].Text, 20)))
			} else if len(where) > 1 {
				add("R-SHOT-001", where[1], fmt.Sprintf("台词 %s 落在 %d 个镜头：%s", lid, len(where), pyList(where)))
			}
		}
		for _, lid := range usedOrder {
			if !inEpisode[lid] {
				add("R-SHOT-001", used[lid][0], fmt.Sprintf("镜头 %s 引用了剧本里不存在的台词 id %s", pyList(used[lid]), pyStrOrNone(lid)))
			}
		}
	} else {
		R.Skipped = append(R.Skipped, CheckSkip{"R-SHOT-001", "读不到剧本"})
	}
	// 逐镜
	maxAxes := rulesP.maxMoveAxes()
	vfxIDs := ctx.vfxIDs()
	var prev *Shot
	sameMoveRun, sameSizeRun := 1, 1
	shotIndex := map[string]int{}
	for i, sh := range shots {
		if _, ok := shotIndex[sh.ID]; !ok {
			shotIndex[sh.ID] = i
		}
	}
	for i := range shots {
		sh := &shots[i]
		cam := sh.Cam
		if z, ok := zOfSize[sh.Size]; ok && cam.Z != "" && z != cam.Z {
			add("R-SHOT-002", sh.ID, fmt.Sprintf("%s 景别「%s」与 cam.z=%s 不一致（应为 %s）", sh.ID, sh.Size, cam.Z, z))
		}
		moves := []string(sh.Move)
		axes := map[string]bool{}
		for _, m := range moves {
			a, ok := moveAxis[m]
			if !ok {
				a = m
			}
			if a != "" {
				axes[a] = true
			}
		}
		nAxes := 0
		for a := range axes {
			switch {
			case a == "rot":
			case a == "xy" || a == "xyz":
				nAxes += len(a)
			default:
				nAxes++
			}
		}
		if axes["rot"] {
			nAxes++
		}
		if nAxes > maxAxes {
			add("R-SHOT-003", sh.ID, fmt.Sprintf("%s 运镜 %s 涉及 %d 轴，超过档案上限 %d", sh.ID, pyList(moves), nAxes, maxAxes))
		}
		if slices.Contains(moves, "fixed") && len(moves) > 1 {
			add("R-SHOT-003", sh.ID, fmt.Sprintf("%s fixed 不能与其他运镜同列：%s", sh.ID, pyList(moves)))
		}
		if boolOr(rulesP.NoRotationAtZ1Z3, true) && (cam.Z == "Z1" || cam.Z == "Z2" || cam.Z == "Z3") && (slices.Contains(moves, "orbit") || slices.Contains(moves, "spiral") || slices.Contains(moves, "roll")) {
			add("R-SHOT-004", sh.ID, fmt.Sprintf("%s 近景（%s）做大旋转 %s，容易崩脸", sh.ID, cam.Z, pyList(moves)))
		}
		if slices.Contains(moves, "push") && slices.Contains(moves, "pull") {
			add("R-SHOT-005", sh.ID, fmt.Sprintf("%s 推进与拉远同时出现", sh.ID))
		}
		if rulesP.OneTakeAllowed != nil && !*rulesP.OneTakeAllowed && slices.Contains(moves, "one_take") {
			add("R-SHOT-016", sh.ID, fmt.Sprintf("%s 档案禁一镜到底，改多镜切换或微运镜", sh.ID))
		}
		blob := sh.Sfx + " " + sh.Voice + " " + sh.Action
		var hits []string
		for _, w := range bgmWords {
			if strings.Contains(blob, w) {
				hits = append(hits, w)
			}
		}
		if len(hits) > 0 {
			add("R-SHOT-017", sh.ID, fmt.Sprintf("%s 出现配乐词 %s", sh.ID, pyList(hits)))
		}
		if screenTextWords.MatchString(sh.Action) && script != nil && len(script.ScreenText) == 0 {
			add("R-SHOT-018", sh.ID, fmt.Sprintf("%s 画面可能出现可读文字，但剧本没有登记 screen_text", sh.ID))
		}
		sc := script.scene(sh.Scene)
		est := EstSeconds(sh, lines)
		if len(sh.Lines) > 0 && est > rulesP.standingTalkMaxSec() && !hasActionVerb(sh.Action) {
			add("R-SHOT-010", sh.ID, fmt.Sprintf("%s 含台词、估时 %s 秒却没有可见动作：加动作或切镜", sh.ID, pyFloatRepr(est)))
		}
		indoor := sc != nil && strings.Contains(runePrefix(sc.Heading, 3), "内")
		var loc *BibleLocation
		if sc != nil {
			loc = bible.location(sc.LocationRef)
		}
		if loc != nil && strings.HasPrefix(loc.SpaceType, "S") {
			indoor = true
		}
		light := sh.light()
		if indoor && boolOr(rulesP.IndoorLightAnchorRequired, true) && light.SourceAnchor == "" {
			add("R-SHOT-011", sh.ID, fmt.Sprintf("%s 室内镜头没有光源锚点", sh.ID))
		}
		if prof.LightPolicy == "practicals_only" && light.Policy == "stylized" {
			add("R-SHOT-011", sh.ID, fmt.Sprintf("%s 档案只许实景光，但 light.policy=stylized", sh.ID))
		}
		if sc != nil && len(sh.Blocking) > 0 {
			anchors := map[string]bool{}
			for _, m := range bible.SpatialMaps {
				if m.Scene == sc.ID {
					for _, p := range m.Positions {
						anchors[p.At] = true
					}
					break
				}
			}
			if loc != nil {
				for _, a := range loc.Anchors {
					anchors[a] = true
				}
			}
			if len(anchors) > 0 {
				var bad []string
				for _, b := range sh.Blocking {
					if !anchors[b.At] {
						bad = append(bad, b.At)
					}
				}
				if len(bad) > 0 {
					add("R-SHOT-012", sh.ID, fmt.Sprintf("%s blocking 用了空间图里没有的锚点：%s", sh.ID, pyList(bad)))
				}
			}
		}
		if sc != nil && sh.Subject == "person" && wideSizes[sh.Size] && len(sc.Characters) >= 2 {
			who := map[string]bool{}
			for _, b := range sh.Blocking {
				who[b.Who] = true
			}
			var missing []string
			for _, c := range sc.Characters {
				if !who[c] {
					missing = append(missing, c)
				}
			}
			if len(missing) > 0 {
				add("R-SHOT-013", sh.ID, fmt.Sprintf("%s A 档景别没写全在场人物位置：%s", sh.ID, pyList(missing)))
			}
		}
		if prev != nil && prev.Scene == sh.Scene {
			if prev.Subject == "person" && sh.Subject == "person" && prev.Focus == sh.Focus && prev.EndState != "" && sh.StartState != "" && strings.TrimSpace(prev.EndState) != strings.TrimSpace(sh.StartState) && !stateChangeAction.MatchString(sh.Action) {
				add("R-SHOT-014", sh.ID, fmt.Sprintf("%s start_state 与上一镜 end_state 不一致，且 action 没写出变化", sh.ID))
			}
			sameSubject := sh.Focus == prev.Focus
			if sameSubject && sizeIndex(sh.Size) >= 0 && sizeIndex(prev.Size) >= 0 {
				gap := abs(sizeIndex(sh.Size) - sizeIndex(prev.Size))
				angleChanged := sh.Cam.X != prev.Cam.X || sh.Cam.Y != prev.Cam.Y
				if gap < rulesP.adjacentSizeMinGap() && !angleChanged && sh.Subject == "person" && prev.Subject == "person" {
					add("R-SHOT-007", sh.ID, fmt.Sprintf("%s 与上一镜同主体、景别只差 %d 级且机位未变：跳切风险", sh.ID, gap))
				}
			}
			if sameMoves(sh.Move, prev.Move) {
				sameMoveRun++
			} else {
				sameMoveRun = 1
			}
			if sh.Size == prev.Size {
				sameSizeRun++
			} else {
				sameSizeRun = 1
			}
			violentOnly := len(sh.Move) > 0
			for _, m := range sh.Move {
				if m != "handheld_violent" {
					violentOnly = false
				}
			}
			if quotas.ConsecutiveSameMoveMax != nil && sameMoveRun > *quotas.ConsecutiveSameMoveMax && !violentOnly {
				add("R-SHOT-008", sh.ID, fmt.Sprintf("%s 连续 %d 镜同一运镜 %s", sh.ID, sameMoveRun, pyListOrNone(sh.Move)))
			}
			if quotas.ConsecutiveSameSizeMax != nil && sameSizeRun > *quotas.ConsecutiveSameSizeMax {
				add("R-SHOT-009", sh.ID, fmt.Sprintf("%s 连续 %d 镜同一景别 %s", sh.ID, sameSizeRun, pyStrOrNone(sh.Size)))
			}
		} else {
			sameMoveRun, sameSizeRun = 1, 1
			var firstPerson *Shot
			for j := range shots {
				if shots[j].Scene == sh.Scene && shots[j].Subject == "person" {
					firstPerson = &shots[j]
					break
				}
			}
			kind := ""
			if sc != nil {
				kind = sc.Kind
				if kind == "" {
					kind = "dialogue"
				}
			}
			if sc != nil && kind == "dialogue" && len(rulesP.DialogueOpenSize) > 0 && firstPerson == sh && !slices.Contains(rulesP.DialogueOpenSize, sh.Size) {
				add("R-SHOT-021", sh.ID, fmt.Sprintf("%s 对话场第一个人物镜景别「%s」不在档案允许的 %s（先交代谁在场）", sh.ID, pyStrOrNone(sh.Size), pyList(rulesP.DialogueOpenSize)))
			}
		}
		if sc != nil && sc.Kind == "action" {
			if sh.Fight == nil || !slices.Contains([]string{"hit", "graze", "blocked", "miss", "none"}, sh.Fight.Contact) {
				add("R-SHOT-019", sh.ID, fmt.Sprintf("%s 动作场镜头缺因果动作单元或接触判定", sh.ID))
			}
		}
		for _, v := range sh.Vfx {
			if len(vfxIDs) > 0 && !vfxIDs[v.ID] {
				add("R-SHOT-020", sh.ID, fmt.Sprintf("%s 特效 %s 不在词库里", sh.ID, v.ID))
			}
		}
		if prof.CameraFollowsEmotion && sh.Subject == "person" && sh.Performance.truthy() && !sh.CameraEmotion.truthy() {
			add("R-SHOT-022", sh.ID, fmt.Sprintf("%s 有表演但没写 camera_emotion（档案要求摄影机跟情绪）", sh.ID))
		}
		if loc != nil {
			pol, ok := prof.SpacePolicy[loc.SpaceType]
			if len(prof.SpacePolicy) == 0 {
				pol, ok = defaultSpacePolicy[loc.SpaceType]
			}
			if ok {
				if pol.MaxSize != "" && sizeIndex(sh.Size) >= 0 && sizeIndex(sh.Size) < sizeIndex(pol.MaxSize) {
					add("R-SHOT-023", sh.ID, fmt.Sprintf("%s 场景 %s 最大景别 %s，用了 %s", sh.ID, loc.SpaceType, pol.MaxSize, sh.Size))
				}
				var banned []string
				for _, m := range pol.No {
					if slices.Contains(moves, m) && !slices.Contains(banned, m) {
						banned = append(banned, m)
					}
				}
				if len(banned) > 0 {
					sort.Strings(banned)
					add("R-SHOT-023", sh.ID, fmt.Sprintf("%s 场景 %s 禁用运镜 %s", sh.ID, loc.SpaceType, pyList(banned)))
				}
				if pol.NoBirdseye && cam.Y == "Y7" {
					add("R-SHOT-023", sh.ID, fmt.Sprintf("%s 室内普通层高禁鸟瞰（Y7）", sh.ID))
				}
			}
		}
		if prof.PerformanceGrain == "micro" && sh.Performance != nil && len(sh.Performance.Beats) == 0 {
			add("R-SHOT-024", sh.ID, fmt.Sprintf("%s 档案要求微表演节拍，performance.beats 为空", sh.ID))
		}
		var ge []string
		for _, w := range genericEmo {
			if strings.Contains(sh.Action, w) {
				ge = append(ge, w)
			}
		}
		if len(ge) > 0 && len(sh.Performance.beats()) == 0 {
			add("R-SHOT-024", sh.ID, fmt.Sprintf("%s action 里有泛情绪词 %s 而无具体可见细节", sh.ID, pyList(ge)))
		}
		// 2.1：情绪弧 / 动作模式 / 衔接 / 台词三元 / 灯光七项
		if sh.Fight != nil && len(sh.Fight.MartialIDs) > 0 && ctx.Vocab != nil {
			var badM []string
			for _, id := range sh.Fight.MartialIDs {
				if ctx.Vocab.MartialEntry(id) == nil {
					badM = append(badM, id)
				}
			}
			if len(badM) > 0 {
				add("R-SHOT-020", sh.ID, fmt.Sprintf("%s fight.martial_ids 里 %s 不在武学词库", sh.ID, pyList(badM)))
			}
		}
		if arc := sh.EmotionArc; arc.truthy() {
			var a *EmotionArc
			if ctx.Vocab != nil {
				a = ctx.Vocab.Arc(arc.ID)
			}
			if a == nil {
				add("R-SHOT-025", sh.ID, fmt.Sprintf("%s 情绪弧 %s 不在词库里", sh.ID, arc.ID))
			} else {
				n := len(a.Phases)
				pf, pt := int(arc.PhaseFrom), int(arc.PhaseTo)
				if (pf != 0 && pf > n) || (pt != 0 && pt > n) || (pf != 0 && pt != 0 && pf > pt) {
					add("R-SHOT-025", sh.ID, fmt.Sprintf("%s 情绪弧 %s 只有 %d 段，phase_from/to=%s/%s 不合法", sh.ID, arc.ID, n, intOrNone(pf), intOrNone(pt)))
				}
			}
			if arc.Continues != "" {
				// 与参考实现一致：按 id 取最后一个同号镜头，比较位置用第一次出现的下标。
				var src *Shot
				for j := range shots {
					if shots[j].ID == arc.Continues {
						src = &shots[j]
					}
				}
				if src == nil || src.Scene != sh.Scene || src.Focus != sh.Focus || !src.EmotionArc.truthy() || shotIndex[src.ID] >= shotIndex[sh.ID] {
					add("R-SHOT-025", sh.ID, fmt.Sprintf("%s continues=%s 不是同场同 focus 且带弧的镜头", sh.ID, arc.Continues))
				}
			}
		} else if prof.EmotionArcRequired && sh.Subject == "person" && (len(sh.Lines) > 0 || sh.Performance.truthy()) && len(sh.Performance.beats()) == 0 {
			add("R-SHOT-026", sh.ID, fmt.Sprintf("%s 含台词或表演但没有 emotion_arc，也没有手写 performance.beats", sh.ID))
		}
		if ap := sh.ActionPattern; ap != nil && (ap.ID != "" || ap.Variant != "" || ap.Role != "") {
			var pat *ActionPattern
			if ctx.Vocab != nil {
				pat = ctx.Vocab.Action(ap.ID)
			}
			if pat == nil {
				add("R-SHOT-028", sh.ID, fmt.Sprintf("%s 动作模式 %s 不在词库里", sh.ID, pyStrOrNone(ap.ID)))
			} else if pat.TwoPerson && len(sh.Blocking) < 2 {
				add("R-SHOT-028", sh.ID, fmt.Sprintf("%s 双人动作模式 %s 用在只有 %d 人站位的镜头", sh.ID, ap.ID, len(sh.Blocking)))
			}
		}
		if tr := sh.Transition; tr != nil && tr.In != "" && prev != nil && prev.Scene == sh.Scene {
			sameSubject := sh.Focus == prev.Focus
			gap := 9
			if sizeIndex(sh.Size) >= 0 && sizeIndex(prev.Size) >= 0 {
				gap = abs(sizeIndex(sh.Size) - sizeIndex(prev.Size))
			}
			angleChanged := sh.Cam.X != prev.Cam.X || sh.Cam.Y != prev.Cam.Y
			if strings.Contains(tr.In, "硬切") && sameSubject && gap < 2 && !angleChanged {
				add("R-SHOT-027", sh.ID, fmt.Sprintf("%s 衔接写了硬切，但与上一镜同主体、景别只差 %d 级且机位未变，是跳切", sh.ID, gap))
			}
			if strings.Contains(tr.In, "动作匹配") && !hasActionVerb(prev.Action) {
				add("R-SHOT-027", sh.ID, fmt.Sprintf("%s 衔接写了动作匹配切，但上一镜 action 里没有动作动词可接", sh.ID))
			}
		}
		for _, ln := range sh.Lines {
			n := 0
			for _, v := range []string{ln.Pace, ln.Tone, ln.Emotion} {
				if v != "" {
					n++
				}
			}
			if n > 0 && n < 3 {
				add("R-SHOT-029", sh.ID, fmt.Sprintf("%s 台词 %s 的三元（语速 / 语调 / 情感）没写齐，编译会退回 delivery", sh.ID, pyStrOrNone(ln.LineID)))
			}
		}
		if prof.LightPolicy == "practicals_only" {
			var bad []string
			for _, kv := range [][2]string{{"fill", light.Fill}, {"rim", light.Rim}, {"key", light.Key}} {
				cleaned := noFillPattern.ReplaceAllString(kv[1], "")
				if containsAny(cleaned, fillWords...) {
					bad = append(bad, kv[0])
				}
			}
			if len(bad) > 0 {
				add("R-SHOT-030", sh.ID, fmt.Sprintf("%s 实景光档案下 light.%s 写了补光设备", sh.ID, strings.Join(bad, "/")))
			}
		}
		prev = sh
	}
	// 伏笔
	for _, p := range bible.Plants {
		if epPrefix != "" && !strings.HasPrefix(p.Scene, epPrefix) {
			continue
		}
		if p.Scene == "" {
			continue
		}
		carried := false
		for _, sh := range shots {
			if sh.Scene == p.Scene && sh.PlantID == p.ID {
				carried = true
			}
		}
		if !carried {
			add("R-SHOT-015", "", fmt.Sprintf("伏笔 %s（%s）在 %s 没有镜头承担", p.ID, p.What, p.Scene))
		}
	}
	// 配额
	n := float64(max(len(shots), 1))
	ratio := func(pred func(*Shot) bool) float64 {
		c := 0
		for i := range shots {
			if pred(&shots[i]) {
				c++
			}
		}
		return float64(c) / n
	}
	type quotaCheck struct {
		name   string
		val    float64
		lo, hi *float64
	}
	var checks []quotaCheck
	if quotas.NormalMax != nil {
		checks = append(checks, quotaCheck{"常规镜头占比", ratio(isNormalShot), nil, quotas.NormalMax})
	}
	if quotas.Side45Min != nil || quotas.Side45Max != nil {
		checks = append(checks, quotaCheck{"侧 45 占比", ratio(func(s *Shot) bool { return s.Cam.X == "X2" }), quotas.Side45Min, quotas.Side45Max})
	}
	if quotas.Side90Max != nil {
		checks = append(checks, quotaCheck{"侧面占比", ratio(func(s *Shot) bool { return s.Cam.X == "X3" }), nil, quotas.Side90Max})
	}
	if quotas.FixedMax != nil {
		checks = append(checks, quotaCheck{"固定镜头占比", ratio(func(s *Shot) bool { return len(s.Move) == 1 && s.Move[0] == "fixed" }), nil, quotas.FixedMax})
	}
	if quotas.CloseupMax != nil {
		checks = append(checks, quotaCheck{"人物特写占比", ratio(func(s *Shot) bool { return closeSizes[s.Size] && s.Subject == "person" }), nil, quotas.CloseupMax})
	}
	if quotas.NonPersonMin != nil || quotas.NonPersonMax != nil {
		checks = append(checks, quotaCheck{"非人物镜头占比", ratio(func(s *Shot) bool { return s.Subject == "non_person" }), quotas.NonPersonMin, quotas.NonPersonMax})
	}
	for _, c := range checks {
		if c.hi != nil && c.val > *c.hi+1e-9 {
			addLevel("R-SHOT-006", "warn", fmt.Sprintf("%s %s 超过档案上限 %s", c.name, pyPercent(c.val), pyPercent(*c.hi)))
		}
		if c.lo != nil && c.val < *c.lo-1e-9 && len(shots) >= 8 {
			addLevel("R-SHOT-006", "warn", fmt.Sprintf("%s %s 低于档案下限 %s", c.name, pyPercent(c.val), pyPercent(*c.lo)))
		}
	}
	runs, last := 0, false
	dutch, low, high := 0, 0, 0
	for i := range shots {
		s := &shots[i]
		if s.camF().Geometry == "dutch" {
			dutch++
		}
		if s.Cam.Y == "Y1" {
			low++
		}
		if s.Cam.Y == "Y7" {
			high++
		}
		cur := slices.Contains(s.Move, "handheld_violent")
		if cur && !last {
			runs++
		}
		last = cur
	}
	for _, c := range []struct {
		label string
		used  int
		cap   *int
	}{{"荷兰角", dutch, quotas.DutchMax}, {"大仰角 / 虫视", low, quotas.ExtremeLowMax}, {"大俯角 / 鸟瞰", high, quotas.ExtremeHighMax}, {"手持剧烈晃动段落", runs, quotas.ShakeViolentMax}} {
		if c.cap != nil && c.used > *c.cap {
			addLevel("R-SHOT-006", "error", fmt.Sprintf("%s 用了 %d 次，档案上限 %d", c.label, c.used, *c.cap))
		}
	}
	for _, hv := range quotas.HeavyMoves {
		used := 0
		for i := range shots {
			s := &shots[i]
			switch hv.Key {
			case "fisheye":
				if s.camF().Distortion == "fisheye" {
					used++
				}
			case "pov":
				if s.camF().Identity == "pov" {
					used++
				}
			default:
				if slices.Contains(s.Move, hv.Key) {
					used++
				}
			}
		}
		if used > hv.Value {
			addLevel("R-SHOT-006", "error", fmt.Sprintf("重技法 %s 用了 %d 次，档案上限 %d", hv.Key, used, hv.Value))
		}
	}
	if quotas.MinAngles != nil && len(shots) >= 12 {
		angles := map[[2]string]bool{}
		for _, s := range shots {
			angles[[2]string{s.Cam.X, s.Cam.Y}] = true
		}
		if len(angles) < *quotas.MinAngles {
			addLevel("R-SHOT-006", "warn", fmt.Sprintf("机位种类 %d，少于档案要求 %d", len(angles), *quotas.MinAngles))
		}
	}
	if quotas.MinMoves != nil && len(shots) >= 12 {
		mv := map[string]bool{}
		for _, s := range shots {
			for _, m := range s.Move {
				mv[m] = true
			}
		}
		if len(mv) < *quotas.MinMoves {
			addLevel("R-SHOT-006", "warn", fmt.Sprintf("运镜种类 %d，少于档案要求 %d", len(mv), *quotas.MinMoves))
		}
	}
	if quotas.GroupWideMin != nil && script != nil {
		for _, sc := range script.Scenes {
			if len(sc.Characters) < 3 {
				continue
			}
			mine, wide := 0, 0
			for _, s := range shots {
				if s.Scene == sc.ID {
					mine++
					if wideSizes[s.Size] && len(s.Blocking) >= 2 {
						wide++
					}
				}
			}
			if mine > 0 {
				w := float64(wide) / float64(mine)
				if w < *quotas.GroupWideMin {
					addLevel("R-SHOT-006", "warn", fmt.Sprintf("%s 三人以上场 A 档群像占比 %s，低于 %s", sc.ID, pyPercent(w), pyPercent(*quotas.GroupWideMin)))
				}
			}
		}
	}
	return R
}

// AddSchemaIssues 把转换和契约问题记成 R-SCHEMA（解析失败是 error，契约外字段是 warn）。
func (r *CheckReport) AddSchemaIssues(issues []Issue) {
	for _, i := range issues {
		r.add(nil, "R-SCHEMA", "", i.Level, i.Message)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func intOrNone(n int) string {
	if n == 0 {
		return "None"
	}
	return fmt.Sprint(n)
}
