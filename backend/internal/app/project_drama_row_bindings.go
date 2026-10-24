// 分镜行按名称落资产绑定：前端写镜头表前跑的 bindStoryboardRowAssets（chapter-storyboard-production.ts）在服务端的同一套规则。
// 服务端评测（M6 第三期）真跑生成分段后用它，装段才与用户在章节页点「生成分段」时一样按场景切场、带上参考。

package app

import (
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

type rowAssetKind string

const (
	rowAssetCharacter rowAssetKind = "character"
	rowAssetScene     rowAssetKind = "scene"
	rowAssetProp      rowAssetKind = "prop"
	rowAssetOther     rowAssetKind = "other"
)

var (
	rowAssetRoleByKind = map[rowAssetKind]string{rowAssetCharacter: "character", rowAssetScene: "environment", rowAssetProp: "prop", rowAssetOther: "style"}
	// rowAssetNameNoise 是资产名比对时忽略的空白、括号、引号和常见标点（模型常写成「七床病房（夜）」「“林小满”」）。
	rowAssetNameNoise  = regexp.MustCompile("[\\s·・,，。、；;：:!！?？\"'“”‘’「」『』《》〈〉()（）\\[\\]【】<>_—-]")
	rowAssetMentionSep = regexp.MustCompile("[、，,;；\n]")
	rowDialogueLines   = regexp.MustCompile(`\n+`)
	rowSpeakerPattern  = regexp.MustCompile(`^\s*([^：:]{1,16})[：:]`)
	rowNarratorPattern = regexp.MustCompile(`(?i)画外|旁白|OS|VO`)
	// rowAssetTextFields 是文字兜底匹配看的字段：首帧画面最能说明镜头在哪，排在最前；台词只认说话人，且只用来认角色。
	rowAssetTextFields = []string{"imageGenerationPrompt", "plotDescription", "performanceBlocking", "videoMotionPrompt", "continuityOut"}
)

type rowAssetEntry struct {
	id, versionID string
	key           string
	kind          rowAssetKind
	character     bool
}

// rowAssetCatalog 是 chapterStoryboardAssets：没归档；角色要有角色卡版本，其余要有主版本；媒体或没出图的场景、道具；最多 60 项。
func rowAssetCatalog(assets []ProjectAssetSummary) []rowAssetEntry {
	var out []rowAssetEntry
	for _, a := range assets {
		if len(out) >= 60 {
			break
		}
		if string(a.Status) == "archived" {
			continue
		}
		category := string(a.Category)
		isCharacter := category == "character"
		switch {
		case isCharacter:
			if a.Character == nil || a.Character.VersionID == "" {
				continue
			}
		case a.MediaType == "image" || a.MediaType == "video" || a.MediaType == "audio":
		case a.MediaType == "entity" && (slices.Contains(chapterShotsSceneCategories, category) || category == "prop"):
		default:
			continue
		}
		if !isCharacter && a.PrimaryVersionID == "" {
			continue
		}
		entry := rowAssetEntry{id: a.ID, key: rowAssetName(a.Title), character: isCharacter}
		switch {
		case isCharacter:
			entry.kind, entry.versionID = rowAssetCharacter, a.Character.VersionID
		case slices.Contains(chapterShotsSceneCategories, category):
			entry.kind = rowAssetScene
		case category == "prop":
			entry.kind = rowAssetProp
		default:
			entry.kind = rowAssetOther
		}
		if entry.key != "" {
			out = append(out, entry)
		}
	}
	return out
}

// rowAssetName 是比对用的资产名：全角字母数字折成半角（前端用 NFKC）、转小写、去掉标点。
func rowAssetName(value string) string {
	folded := strings.Map(func(r rune) rune {
		if r >= 0xFF01 && r <= 0xFF5E {
			return r - 0xFEE0
		}
		if r == 0x3000 {
			return ' '
		}
		return r
	}, value)
	return rowAssetNameNoise.ReplaceAllString(strings.ToLower(folded), "")
}

// bindDramaRowAssets 给分镜行落资产绑定：模型在 assets / characters / assetBindings 里给的资产名或 ID 先落到 assetBindings；
// 角色、场景、道具中模型某一类一个都没给时，按镜头文字里出现的资产名补这一类（场景只补最先出现的那个）。
// characters 里的名字补上角色资产 ID。rows 原地改写。
func bindDramaRowAssets(rows []map[string]any, assets []ProjectAssetSummary) {
	catalog := rowAssetCatalog(assets)
	for _, row := range rows {
		var given []*rowAssetEntry
		for _, mention := range rowAssetMentions(row) {
			if entry := resolveRowAssetMention(mention, catalog); entry != nil && !slices.Contains(given, entry) {
				given = append(given, entry)
			}
		}
		givenKinds := map[rowAssetKind]bool{}
		for _, e := range given {
			givenKinds[e.kind] = true
		}
		var fallback []*rowAssetEntry
		sceneTaken := false
		for _, e := range rowTextAssetMatches(row, catalog) {
			if givenKinds[e.kind] {
				continue
			}
			if e.kind == rowAssetScene {
				if sceneTaken {
					continue
				}
				sceneTaken = true
			}
			fallback = append(fallback, e)
		}
		var bindings []map[string]any
		seen := map[string]bool{}
		push := func(e *rowAssetEntry, priority int) {
			if seen[e.id] {
				return
			}
			seen[e.id] = true
			bindings = append(bindings, map[string]any{"nodeId": e.id, "role": rowAssetRoleByKind[e.kind], "priority": priority})
		}
		for i, e := range given {
			push(e, max(51, 100-i))
		}
		for i, e := range fallback {
			push(e, max(1, 50-i))
		}
		slices.SortStableFunc(bindings, func(a, b map[string]any) int { return b["priority"].(int) - a["priority"].(int) })
		row["assetBindings"] = bindings
		row["characters"] = bindRowCharacters(row["characters"], catalog)
	}
}

func bindRowCharacters(value any, catalog []rowAssetEntry) []any {
	items, _ := value.([]any)
	out := []any{}
	for _, item := range items {
		var ref map[string]any
		switch v := item.(type) {
		case string:
			ref = map[string]any{"characterName": strings.TrimSpace(v)}
		case map[string]any:
			ref = v
		default:
			continue
		}
		name, _ := ref["characterName"].(string)
		assetID, _ := ref["characterAssetId"].(string)
		if strings.TrimSpace(name) == "" && assetID == "" {
			continue
		}
		if assetID == "" {
			if entry := resolveRowAssetMention(name, catalog); entry != nil && entry.kind == rowAssetCharacter {
				bound := map[string]any{}
				for k, v := range ref {
					bound[k] = v
				}
				bound["characterAssetId"] = entry.id
				if entry.versionID != "" {
					bound["characterVersionId"] = entry.versionID
				}
				ref = bound
			}
		}
		out = append(out, ref)
	}
	return out
}

// rowAssetMentions 是模型给出的资产：新契约的 assets、characters 里的角色名，以及偶尔直接写出的 assetBindings。
func rowAssetMentions(row map[string]any) []string {
	var listed []any
	switch v := row["assets"].(type) {
	case []any:
		listed = v
	case string:
		for _, part := range rowAssetMentionSep.Split(v, -1) {
			listed = append(listed, part)
		}
	}
	if characters, ok := row["characters"].([]any); ok {
		listed = append(listed, characters...)
	}
	if bindings, ok := row["assetBindings"].([]any); ok {
		for _, b := range bindings {
			if m, ok := b.(map[string]any); ok {
				listed = append(listed, m["nodeId"])
			} else {
				listed = append(listed, nil)
			}
		}
	}
	var out []string
	for _, item := range listed {
		var text string
		switch v := item.(type) {
		case string:
			text = v
		case map[string]any:
			for _, key := range []string{"id", "assetId", "characterAssetId", "name", "title", "characterName"} {
				if s, ok := v[key].(string); ok {
					text = s
					break
				}
			}
		}
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	return out
}

// resolveRowAssetMention：资产 ID 精确匹配，其次名称（忽略标点）相同；都不中时取名称互相包含且唯一的那个。
func resolveRowAssetMention(mention string, catalog []rowAssetEntry) *rowAssetEntry {
	for i := range catalog {
		if catalog[i].id == mention {
			return &catalog[i]
		}
	}
	key := rowAssetName(mention)
	if key == "" {
		return nil
	}
	for i := range catalog {
		if catalog[i].key == key {
			return &catalog[i]
		}
	}
	var inside []*rowAssetEntry
	longest := 0
	for i := range catalog {
		if n := utf8.RuneCountInString(catalog[i].key); n >= 2 && strings.Contains(key, catalog[i].key) {
			inside = append(inside, &catalog[i])
			longest = max(longest, n)
		}
	}
	var best []*rowAssetEntry
	for _, e := range inside {
		if utf8.RuneCountInString(e.key) == longest {
			best = append(best, e)
		}
	}
	if len(best) == 1 {
		return best[0]
	}
	if utf8.RuneCountInString(key) < 2 {
		return nil
	}
	var around []*rowAssetEntry
	for i := range catalog {
		if strings.Contains(catalog[i].key, key) {
			around = append(around, &catalog[i])
		}
	}
	if len(around) == 1 {
		return around[0]
	}
	return nil
}

// rowTextAssetMatches 是镜头文字里出现的资产名，按出现先后；长名先匹配并遮住，「病房走廊」不会再算成「病房」。
func rowTextAssetMatches(row map[string]any, catalog []rowAssetEntry) []*rowAssetEntry {
	var candidates []*rowAssetEntry
	for i := range catalog {
		if catalog[i].kind != rowAssetOther && utf8.RuneCountInString(catalog[i].key) >= 2 {
			candidates = append(candidates, &catalog[i])
		}
	}
	slices.SortStableFunc(candidates, func(a, b *rowAssetEntry) int {
		return utf8.RuneCountInString(b.key) - utf8.RuneCountInString(a.key)
	})
	dialogue, _ := row["dialogue"].(string)
	var speakers []string
	for _, line := range rowDialogueLines.Split(dialogue, -1) {
		if m := rowSpeakerPattern.FindStringSubmatch(line); m != nil && !rowNarratorPattern.MatchString(m[1]) {
			speakers = append(speakers, m[1])
		}
	}
	type field struct {
		text          string
		characterOnly bool
	}
	fields := make([]field, 0, len(rowAssetTextFields)+1)
	for _, name := range rowAssetTextFields {
		text, _ := row[name].(string)
		fields = append(fields, field{text: text})
	}
	fields = append(fields, field{text: strings.Join(speakers, "\n"), characterOnly: true})
	rank := map[*rowAssetEntry]int{}
	var order []*rowAssetEntry
	for fieldIndex, f := range fields {
		normalized := rowAssetName(f.text)
		for _, entry := range candidates {
			if f.characterOnly && entry.kind != rowAssetCharacter {
				continue
			}
			index := strings.Index(normalized, entry.key)
			if index < 0 {
				continue
			}
			position := fieldIndex*100_000 + index
			if old, ok := rank[entry]; !ok || position < old {
				if !ok {
					order = append(order, entry)
				}
				rank[entry] = position
			}
			normalized = strings.ReplaceAll(normalized, entry.key, strings.Repeat("\x00", len(entry.key)))
		}
	}
	slices.SortStableFunc(order, func(a, b *rowAssetEntry) int { return rank[a] - rank[b] })
	return order
}
