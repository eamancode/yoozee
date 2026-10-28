package drama

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// 资产设计的角色子卡与色卡约束（M8 卡 D）：同一角色换了服装（costume）或状态（state）时，角色行的 variants 每套写一项，
// 收尾建成独立的子卡资产（「名·变体」，继承主卡的脸型体型，只换服装 / 状态）；外观里写到的 HEX 要在对应色卡里
// （角色、道具查主基调色卡，场景查本场色卡和主基调色卡）。

// 子卡的两种变体。
const (
	AssetVariantCostume = "costume"
	AssetVariantState   = "state"
)

var assetVariantKindNames = map[string]string{AssetVariantCostume: "服装", AssetVariantState: "状态"}

// AssetVariantKindName 是子卡变体的中文名（服装 / 状态）；认不出返回空串。
func AssetVariantKindName(kind string) string { return assetVariantKindNames[kind] }

var assetVariantKindAliases = map[string]string{
	"costume": AssetVariantCostume, "outfit": AssetVariantCostume, "clothing": AssetVariantCostume, "wardrobe": AssetVariantCostume, "服装": AssetVariantCostume, "换装": AssetVariantCostume,
	"state": AssetVariantState, "condition": AssetVariantState, "status": AssetVariantState, "状态": AssetVariantState,
}

// assetStateWords 是变体名里一看就是状态（不是换衣服）的字。
var assetStateWords = []string{"伤", "血", "湿", "淋", "破", "脏", "病", "醉", "哭", "泥", "灰", "焦", "烧", "汗", "肿", "残"}

// NormalizeAssetVariantKind 把常见写法规整成 costume / state；认不出时按变体名猜（带伤、湿、破这类字的算状态，其余算服装）。
func NormalizeAssetVariantKind(kind string, label string) string {
	if v, ok := assetVariantKindAliases[strings.ToLower(jsTrim(kind))]; ok {
		return v
	}
	for _, w := range assetStateWords {
		if strings.Contains(label, w) {
			return AssetVariantState
		}
	}
	return AssetVariantCostume
}

// AssetsVariant 是角色的一张子卡：Scenes 是用这套形象的场序（本单元，从 1 数，与 first_scene 同一套）。
type AssetsVariant struct {
	Label      string `json:"label"`
	Kind       string `json:"kind"`
	Scenes     []int  `json:"scenes"`
	Appearance string `json:"appearance"`
	PromptEn   string `json:"prompt_en"`
}

// AssetVariantTitle 是子卡的资产名：「主卡名·变体名」。
func AssetVariantTitle(name string, label string) string { return jsTrim(name) + "·" + jsTrim(label) }

// SplitAssetVariantTitle 把「林小满·淋湿」拆成主卡名和变体名；不是这种写法时 ok 为 false。
func SplitAssetVariantTitle(title string) (name string, label string, ok bool) {
	for _, sep := range []string{"·", "・", "•"} {
		if name, label, found := strings.Cut(jsTrim(title), sep); found && jsTrim(name) != "" && jsTrim(label) != "" {
			return jsTrim(name), jsTrim(label), true
		}
	}
	return "", "", false
}

// parseAssetsVariants 读角色行的 variants：数组，每项是对象；scenes 写成数字、「2」「S02」「E01S02」或一句话都认。
func parseAssetsVariants(raw json.RawMessage) []AssetsVariant {
	var items []map[string]json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return nil
	}
	var out []AssetsVariant
	for _, obj := range items {
		str := func(key string) string {
			var v any
			_ = json.Unmarshal(obj[key], &v)
			if s, ok := v.(string); ok {
				return jsTrim(s)
			}
			return ""
		}
		v := AssetsVariant{Label: str("label"), Appearance: str("appearance"), PromptEn: str("prompt_en")}
		v.Kind = NormalizeAssetVariantKind(str("kind"), v.Label)
		v.Scenes = assetsSceneList(obj["scenes"])
		out = append(out, v)
	}
	return out
}

// assetsSceneList 读场序列表：数组（数字或字符串）或一句话，去掉读不出的、去重、按先后。
func assetsSceneList(raw json.RawMessage) []int {
	var v any
	_ = json.Unmarshal(raw, &v)
	var items []any
	switch x := v.(type) {
	case []any:
		items = x
	case string:
		for _, part := range assetsAliasSplit.Split(x, -1) {
			items = append(items, part)
		}
	case float64:
		items = []any{x}
	}
	out := []int{}
	for _, item := range items {
		if n := assetsSceneNo(item); n > 0 && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// variantFromTitledRow 把模型单独写成一行的子卡（角色名写成「林小满·淋湿」）改成主卡行的一项 variants。
func variantFromTitledRow(r AssetsExtractRow) (AssetsExtractRow, bool) {
	if r.Category != AssetCharacter {
		return r, false
	}
	name, label, ok := SplitAssetVariantTitle(r.Name)
	if !ok {
		return r, false
	}
	v := AssetsVariant{Label: label, Kind: NormalizeAssetVariantKind("", label), Appearance: r.Appearance, PromptEn: r.PromptEn}
	if r.FirstScene > 0 {
		v.Scenes = []int{r.FirstScene}
	}
	return AssetsExtractRow{Category: AssetCharacter, Name: name, Aliases: []string{}, FirstScene: r.FirstScene, Variants: append([]AssetsVariant{v}, r.Variants...)}, true
}

// AssetVariantKey 是变体名的比对键（与资产名同一规则）。
func AssetVariantKey(label string) string { return AssetNameKey(label) }

// mergeAssetsVariants 把 next 的子卡并进 prev：同名的合并场序、空字段用后面的补，新的接在后面。
func mergeAssetsVariants(prev []AssetsVariant, next []AssetsVariant) []AssetsVariant {
	for _, v := range next {
		i := slices.IndexFunc(prev, func(p AssetsVariant) bool { return AssetVariantKey(p.Label) == AssetVariantKey(v.Label) })
		if i < 0 {
			prev = append(prev, v)
			continue
		}
		p := &prev[i]
		for _, n := range v.Scenes {
			if !slices.Contains(p.Scenes, n) {
				p.Scenes = append(p.Scenes, n)
			}
		}
		if p.Appearance == "" {
			p.Appearance = v.Appearance
		}
		if p.PromptEn == "" {
			p.PromptEn = v.PromptEn
		}
	}
	return prev
}

// checkAssetsVariants 整理一行角色的子卡：变体名为空、和主卡同名的不要（warn）；外观和提示词都没写的，是已有子卡（known 认得，
// 只补场序）就留着，否则不建（warn）；场序超出本单元的去掉（warn）；同名的并成一项。不是角色的行写了 variants 时整组不要。
func checkAssetsVariants(r *AssetsExtractRow, scenes int, known func(label string) bool, add func(level, rule, name, message string)) {
	if len(r.Variants) == 0 {
		return
	}
	if r.Category != AssetCharacter {
		add("warn", RuleAssetVariant, r.Name, "只有角色能拆子卡，variants 没有用")
		r.Variants = nil
		return
	}
	var kept []AssetsVariant
	for _, v := range r.Variants {
		v.Label = jsTruncate(jsCompact(v.Label), assetsVariantLabelChars)
		label := AssetVariantTitle(r.Name, firstNonEmptyString(v.Label, "（无名）"))
		switch {
		case AssetVariantKey(v.Label) == "" || AssetVariantKey(v.Label) == AssetNameKey(r.Name):
			add("warn", RuleAssetVariant, label, "子卡没写变体名（如 护士服、淋湿），没有建")
			continue
		case v.Appearance == "" && v.PromptEn == "" && !known(v.Label):
			add("warn", RuleAssetVariant, label, "子卡没写这套服装 / 状态的外观和提示词，没有建")
			continue
		}
		if scenes > 0 {
			inRange := v.Scenes[:0:0]
			for _, n := range v.Scenes {
				if n <= scenes {
					inRange = append(inRange, n)
				}
			}
			if len(inRange) < len(v.Scenes) {
				add("warn", RuleAssetVariant, label, fmt.Sprintf("scenes 写了本单元没有的场（本单元只有 %d 场），已去掉", scenes))
			}
			v.Scenes = inRange
		}
		v.Kind = NormalizeAssetVariantKind(v.Kind, v.Label)
		kept = mergeAssetsVariants(kept, []AssetsVariant{v})
	}
	r.Variants = kept
}

// ---------------------------------------------------------------- 色卡约束

var assetsHexPattern = regexp.MustCompile(`#[0-9A-Fa-f]{6}\b`)

// AssetsHexes 是一段文字里写到的 HEX（大写、去重、按先后）。
func AssetsHexes(texts ...string) []string {
	var out []string
	for _, text := range texts {
		for _, m := range assetsHexPattern.FindAllString(text, -1) {
			if hex := strings.ToUpper(m); !slices.Contains(out, hex) {
				out = append(out, hex)
			}
		}
	}
	return out
}

// AssetMatchesLocation 是场景资产（名字或别名）对得上场次头里的地点：比对键相同，或一方包含另一方且至少两个字。
func AssetMatchesLocation(name string, aliases []string, location string) bool {
	loc := AssetNameKey(locationCore(location))
	if len([]rune(loc)) == 0 {
		return false
	}
	for _, n := range append([]string{name}, aliases...) {
		key := AssetNameKey(locationCore(n))
		if key == "" {
			continue
		}
		if key == loc || (len([]rune(key)) >= 2 && len([]rune(loc)) >= 2 && (strings.Contains(key, loc) || strings.Contains(loc, key))) {
			return true
		}
	}
	return false
}

// locationCore 去掉括注（「电梯口（夜）」→「电梯口」）。
func locationCore(name string) string {
	if i := strings.IndexAny(name, "（("); i > 0 {
		name = name[:i]
	}
	return jsTrim(name)
}

// assetsAllowedHexes 是这一行外观里可以写的 HEX：角色、道具是主基调色卡；场景是主基调色卡加它出现的各场的色卡
// （场次头地点对得上它的场和 first_scene；一场都认不出时是本集全部场次色卡）。都没有色卡时返回 nil（不查）。
func assetsAllowedHexes(r AssetsExtractRow, ctx AssetsExtractCheckContext) map[string]bool {
	allowed := map[string]bool{}
	for _, c := range ctx.Master.Colors() {
		allowed[strings.ToUpper(c.Hex)] = true
	}
	if r.Category == AssetEnvironment && len(ctx.ColorScript) > 0 {
		scenes := map[int]bool{}
		if r.FirstScene > 0 {
			scenes[r.FirstScene] = true
		}
		for i, loc := range ctx.SceneLocations {
			if AssetMatchesLocation(r.Name, r.Aliases, loc) {
				scenes[i+1] = true
			}
		}
		for _, e := range ctx.ColorScript {
			if len(scenes) == 0 || scenes[e.SceneNo] {
				for _, c := range e.Colors() {
					allowed[strings.ToUpper(c.Hex)] = true
				}
			}
		}
	}
	if len(allowed) == 0 {
		return nil
	}
	return allowed
}

// checkAssetsHexes 是 R-ASSET-009：外观、出图提示词（含子卡）里写的 HEX 不在对应色卡里（warn）。
func checkAssetsHexes(r AssetsExtractRow, ctx AssetsExtractCheckContext, add func(level, rule, name, message string)) {
	allowed := assetsAllowedHexes(r, ctx)
	if allowed == nil {
		return
	}
	palette := "主基调色卡"
	if r.Category == AssetEnvironment {
		palette = "本场色卡和主基调色卡"
	}
	check := func(label string, texts ...string) {
		var stray []string
		for _, hex := range AssetsHexes(texts...) {
			if !allowed[hex] {
				stray = append(stray, hex)
			}
		}
		if len(stray) > 0 {
			add("warn", RuleAssetPaletteHex, label, fmt.Sprintf("外观里的 %s 不在%s里", strings.Join(stray, "、"), palette))
		}
	}
	check(r.Name, r.Appearance, r.PromptEn)
	for _, v := range r.Variants {
		check(AssetVariantTitle(r.Name, v.Label), v.Appearance, v.PromptEn)
	}
}

// assetsMasterPrompt 是【主基调色卡】段（标明用于角色、道具）：没有主色卡时不写。
func assetsMasterPrompt(master MasterPalette) string {
	return MasterPalettePrompt(master, "锁定风格时定的全片主基调色卡，用于角色和道具：角色服装、饰品和道具的颜色从对应分组里取，同名颜色 HEX 照抄；场景以环境组为基础色。")
}

// assetsColorRulePrompt 是【用色要求】：有色卡时写明各类资产从哪张色卡取色、外观里写出色名和 HEX。
func assetsColorRulePrompt(in AssetsExtractInput) string {
	if in.Master.Empty() && len(in.ColorScript) == 0 {
		return ""
	}
	return "【用色要求】角色服装与道具用色从【主基调色卡】取；场景用色从本场色卡（【本集场次色卡】里这一场）与主基调色卡取。" +
		"appearance、prompt_en（含子卡）写到颜色时写色名加 HEX（如「护士蓝 #5B7FA3」），HEX 照抄色卡，不写色卡以外的 HEX；palette_hint 写用到的色名。"
}
