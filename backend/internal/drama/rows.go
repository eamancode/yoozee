package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// 柚子分镜行（StoryboardRow，带或不带 spec）→ 镜头表（与 .local/yozi-ab/run/rows_to_shotlist.py 一致）：
// 台词按原文对回剧本 line_id；去标点后同文的台词（「七床？」「七床。」）先按说话人、再按场号消歧。
// 没有 spec 的行用平铺字段兜底成一个镜头，检查会把缺的字段报出来。

// RowAssetBinding 是分镜行绑定的资产（nodeId 是资产 ID）。
type RowAssetBinding struct {
	NodeID   string  `json:"nodeId"`
	Role     string  `json:"role"`
	Priority float64 `json:"priority"`
}

// StoryboardRow 是前端分镜行里装段会用到的字段；其余字段忽略。
type StoryboardRow struct {
	ShotNumber      int               `json:"shotNumber"`
	DurationSeconds flexFloat         `json:"durationSeconds"`
	PlotDescription string            `json:"plotDescription"`
	Dialogue        string            `json:"dialogue"`
	ShotSize        string            `json:"shotSize"`
	AssetBindings   []RowAssetBinding `json:"assetBindings"`
	Spec            json.RawMessage   `json:"spec"`
}

// HasSpec 这一行有没有镜头表 spec（JSON 对象）。
func (r StoryboardRow) HasSpec() bool {
	raw := bytes.TrimSpace(r.Spec)
	return len(raw) > 0 && raw[0] == '{'
}

// RowsStats 是转换统计（与 rows_to_shotlist.py 的输出一致）。
type RowsStats struct {
	Rows          int `json:"rows"`
	Shots         int `json:"shots"`
	NoSpec        int `json:"no_spec,omitempty"`
	LineExact     int `json:"line_exact,omitempty"`
	LineFuzzy     int `json:"line_fuzzy,omitempty"`
	LineUnmatched int `json:"line_unmatched,omitempty"`
	SceneRemap    int `json:"scene_remap,omitempty"`
	SceneUnknown  int `json:"scene_unknown,omitempty"`
	DupID         int `json:"dup_id,omitempty"`
}

// RowsResult 是分镜行转出的镜头表与转换中发现的契约问题。
type RowsResult struct {
	Shotlist     Shotlist  `json:"shotlist"`
	Stats        RowsStats `json:"stats"`
	SchemaIssues []Issue   `json:"schemaIssues,omitempty"`
}

var rowDialoguePattern = regexp.MustCompile(`^[\s\p{Z}]*([^：:]{1,16})[：:][\s\p{Z}]*(.+)$`)

// normLine 去掉空白和标点再比台词（rows_to_shotlist.py 的 norm）。
func normLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("，。！？、“”‘’…—,.!?:：;；~～-", r) {
			return -1
		}
		return r
	}, s)
}

type lineCandidate struct {
	id, speaker, scene string
}

func pickLine(cands []lineCandidate, speaker, scene string) string {
	if len(cands) == 1 {
		return cands[0].id
	}
	for _, key := range []string{"speaker", "scene"} {
		want := speaker
		if key == "scene" {
			want = scene
		}
		var sub []lineCandidate
		for _, c := range cands {
			got := c.speaker
			if key == "scene" {
				got = c.scene
			}
			if want != "" && got == want {
				sub = append(sub, c)
			}
		}
		if len(sub) == 1 {
			return sub[0].id
		}
		if len(sub) > 0 {
			cands = sub
		}
	}
	if len(cands) > 0 {
		return cands[0].id
	}
	return ""
}

// RowsToShotlist 把分镜行转成镜头表（一行一镜）。profile 为空时用默认档案。
func RowsToShotlist(rows []StoryboardRow, script *Script, profile string) RowsResult {
	if profile == "" {
		profile = DefaultProfileID
	}
	var textOrder []string
	byText := map[string][]lineCandidate{}
	var sceneIDs []string
	if script != nil {
		for _, sc := range script.Scenes {
			sceneIDs = append(sceneIDs, sc.ID)
			for _, b := range sc.Beats {
				if !b.isLine() {
					continue
				}
				key := normLine(b.Text)
				if _, ok := byText[key]; !ok {
					textOrder = append(textOrder, key)
				}
				byText[key] = append(byText[key], lineCandidate{b.ID, b.Speaker, sc.ID})
			}
		}
	}
	lines := newScriptLines(script)
	stats := RowsStats{Rows: len(rows)}
	var schema []Issue
	shots := make([]Shot, 0, len(rows))
	for i, row := range rows {
		n := i + 1
		var spec Shot
		parsed := false
		if row.HasSpec() {
			sh, unknown, err := ParseShot(row.Spec)
			if err != nil {
				schema = append(schema, Issue{"error", fmt.Sprintf("第 %d 行的 spec 不是合法镜头：%s", n, err.Error())})
			} else {
				spec, parsed = sh, true
				if len(unknown) > 0 {
					schema = append(schema, Issue{"warn", fmt.Sprintf("shots 不符合契约：第 %d 行（%s）有契约外的字段 %s", n, firstNonEmptyString(sh.ID, "无 id"), pyList(unknown))})
				}
			}
		}
		if !parsed {
			stats.NoSpec++
			est := float64(row.DurationSeconds)
			if est == 0 {
				est = 3
			}
			spec = Shot{ID: fmt.Sprintf("E01S00-%02d", n), Scene: "E01S00", Size: row.ShotSize, Action: row.PlotDescription, Est: flexFloat(est)}
			for _, ln := range strings.Split(row.Dialogue, "\n") {
				if m := rowDialoguePattern.FindStringSubmatch(ln); m != nil {
					spec.Lines = append(spec.Lines, ShotLine{Speaker: m[1], Text: m[2]})
				}
			}
		}
		var outLines []ShotLine
		for _, ln := range spec.Lines {
			t := ln.Text
			if normLine(t) == "" {
				// 只写了 line_id、没写原文的台词：id 在剧本里就保留，否则丢掉（与参考实现一致）。
				if _, ok := lines.byID[ln.LineID]; ok && ln.LineID != "" {
					ln.Speaker = ""
					outLines = append(outLines, ln)
					stats.LineExact++
				}
				continue
			}
			speaker := firstNonEmptyString(ln.Speaker, spec.Focus)
			lid := pickLine(byText[normLine(t)], speaker, spec.Scene)
			if lid == "" {
				var cands []lineCandidate
				key := normLine(t)
				for _, k := range textOrder {
					if strings.Contains(k, key) || strings.Contains(key, k) {
						cands = append(cands, byText[k]...)
					}
				}
				if len(cands) > 0 {
					lid = pickLine(cands, speaker, spec.Scene)
				}
				if lid != "" {
					stats.LineFuzzy++
				} else {
					stats.LineUnmatched++
				}
			} else {
				stats.LineExact++
			}
			d := ln
			d.Text, d.Speaker = "", ""
			if lid != "" {
				d.LineID = lid
			} else {
				d.Text, d.Speaker = t, speaker
			}
			outLines = append(outLines, d)
		}
		spec.Lines = outLines
		// 场号：有台词就跟台词的场走；否则信模型。
		counts := map[string]int{}
		var order []string
		for _, l := range outLines {
			if l.LineID == "" {
				continue
			}
			sc := l.LineID
			if len(sc) > 6 {
				sc = sc[:6]
			}
			if counts[sc] == 0 {
				order = append(order, sc)
			}
			counts[sc]++
		}
		if len(order) > 0 {
			best := order[0]
			for _, sc := range order {
				if counts[sc] > counts[best] {
					best = sc
				}
			}
			if spec.Scene != best {
				stats.SceneRemap++
			}
			spec.Scene = best
		} else if !containsString(sceneIDs, spec.Scene) {
			stats.SceneUnknown++
		}
		if spec.Est == 0 {
			est := float64(row.DurationSeconds)
			if est == 0 {
				est = 3
			}
			spec.Est = flexFloat(est)
		}
		if spec.ID == "" {
			spec.ID = fmt.Sprintf("%s-%02d", spec.Scene, n)
		}
		shots = append(shots, spec)
	}
	idCount := map[string]int{}
	for _, s := range shots {
		idCount[s.ID]++
	}
	for i := range shots {
		if idCount[shots[i].ID] > 1 {
			stats.DupID++
			shots[i].ID = fmt.Sprintf("%s-%02dx", shots[i].Scene, i+1)
		}
	}
	stats.Shots = len(shots)
	episode := 1
	return RowsResult{Shotlist: Shotlist{Profile: profile, Episode: &episode, Shots: shots}, Stats: stats, SchemaIssues: schema}
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
