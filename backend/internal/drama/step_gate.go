package drama

import (
	"fmt"
	"math"
)

// 自动启用门槛（M6 第三期）：包上传后在评测集上真跑这一步，逐章得出下面的指标，再按步骤表里的 StepGate 判定；
// warn 跟当前启用版本在同一组章节上的结果比。判定只看数字，跑任务、取结果在 app 层。

// StepEvalChapterResult 是一章真跑这一步的结果。
type StepEvalChapterResult struct {
	Chapter string `json:"chapter"`
	Label   string `json:"label"`
	Profile string `json:"profile"`
	TaskID  string `json:"taskId,omitempty"`
	// Parsed 是模型输出能解析出镜头行；Truncated 是输出达到上限被截断；Failure 是没得到可用结果的原因（任务失败、解析失败）。
	Parsed    bool   `json:"parsed"`
	Truncated bool   `json:"truncated"`
	Failure   string `json:"failure,omitempty"`
	Rows      int    `json:"rows"`
	// SpecRows 是写了合法 spec 的行数。
	SpecRows   int `json:"specRows"`
	LinesTotal int `json:"linesTotal"`
	LinesExact int `json:"linesExact"`
	// Errors / Warnings 是镜头表检查（含契约问题和分段合计 R-SEG-008）的条数；Rules 按规则编号计数，给逐章差异用。
	Errors   int            `json:"errors"`
	Warnings int            `json:"warnings"`
	Rules    map[string]int `json:"rules,omitempty"`
	// EstSeconds 是镜头估时合计；TargetSeconds 是这一章的每集时长（0 表示没设）。
	EstSeconds    float64 `json:"estSeconds"`
	TargetSeconds float64 `json:"targetSeconds"`
	Shots         int     `json:"shots"`
	// Segments / SegmentSeconds / PackProblems 是装段结果（检查有 error 时不装段）。
	Segments       int     `json:"segments"`
	SegmentSeconds float64 `json:"segmentSeconds"`
	PackProblems   int     `json:"packProblems"`
}

// 门槛指标的编号（前端按它显示名称和单位）。
const (
	GateParseRate        = "parseRate"
	GateSpecRate         = "specRate"
	GateDialogueCoverage = "dialogueCoverage"
	GateCheckErrors      = "checkErrors"
	GateCheckWarnings    = "checkWarnings"
	GateDurationRatio    = "durationRatio"
	GateTruncated        = "truncated"
)

// StepGateCheck 是一项门槛的判定。Value / Limit 是比率（0～1 或倍数）或条数；Baseline 是对照版本的值（warn 才有）。
type StepGateCheck struct {
	Metric   string   `json:"metric"`
	Label    string   `json:"label"`
	Value    float64  `json:"value"`
	Limit    float64  `json:"limit"`
	Baseline *float64 `json:"baseline,omitempty"`
	Passed   bool     `json:"passed"`
	Detail   string   `json:"detail"`
}

// StepGateVerdict 是门槛结论：全部达标才算通过。
type StepGateVerdict struct {
	Passed bool            `json:"passed"`
	Checks []StepGateCheck `json:"checks"`
}

const gateEpsilon = 1e-9

// Evaluate 按门槛判定候选版本的评测结果。baseline 是当前启用版本在同一组章节上的结果（按章节对上；没有就判 warn 不过）。
func (g StepGate) Evaluate(candidate []StepEvalChapterResult, baseline []StepEvalChapterResult) StepGateVerdict {
	var parsed, truncated, rows, specRows, linesTotal, linesExact, errs, warns int
	for _, r := range candidate {
		if r.Parsed {
			parsed++
		}
		if r.Truncated {
			truncated++
		}
		rows += r.Rows
		specRows += r.SpecRows
		linesTotal += r.LinesTotal
		linesExact += r.LinesExact
		errs += r.Errors
		warns += r.Warnings
	}
	chapters := len(candidate)
	out := StepGateVerdict{Passed: true}
	add := func(c StepGateCheck) {
		out.Checks = append(out.Checks, c)
		out.Passed = out.Passed && c.Passed
	}
	rate := func(n, d int, empty float64) float64 {
		if d == 0 {
			return empty
		}
		return float64(n) / float64(d)
	}
	parseRate := rate(parsed, chapters, 0)
	add(StepGateCheck{Metric: GateParseRate, Label: "解析成功率", Value: parseRate, Limit: g.ParseRate, Passed: chapters > 0 && parseRate+gateEpsilon >= g.ParseRate,
		Detail: fmt.Sprintf("%d / %d 章解析出镜头行", parsed, chapters)})
	specRate := rate(specRows, rows, 0)
	add(StepGateCheck{Metric: GateSpecRate, Label: "spec 写出率", Value: specRate, Limit: g.SpecRate, Passed: rows > 0 && specRate+gateEpsilon >= g.SpecRate,
		Detail: fmt.Sprintf("%d / %d 行写了合法 spec", specRows, rows)})
	coverage := rate(linesExact, linesTotal, 1)
	add(StepGateCheck{Metric: GateDialogueCoverage, Label: "台词逐字覆盖", Value: coverage, Limit: g.DialogueCoverage, Passed: coverage+gateEpsilon >= g.DialogueCoverage,
		Detail: fmt.Sprintf("%d / %d 句台词逐字落进镜头", linesExact, linesTotal)})
	add(StepGateCheck{Metric: GateCheckErrors, Label: "检查 error", Value: float64(errs), Limit: float64(g.MaxCheckErrors), Passed: errs <= g.MaxCheckErrors,
		Detail: fmt.Sprintf("合计 %d 条", errs)})
	add(g.warnCheck(candidate, baseline, warns))
	add(g.durationCheck(candidate))
	add(StepGateCheck{Metric: GateTruncated, Label: "截断", Value: float64(truncated), Limit: float64(g.MaxTruncated), Passed: truncated <= g.MaxTruncated,
		Detail: fmt.Sprintf("%d 章输出达到上限被截断", truncated)})
	return out
}

// warnCheck 是检查 warn 不超过当前版本的若干倍：只比两边都解析出来的章节，任一章缺对照就不过。
func (g StepGate) warnCheck(candidate, baseline []StepEvalChapterResult, total int) StepGateCheck {
	check := StepGateCheck{Metric: GateCheckWarnings, Label: "检查 warn", Value: float64(total)}
	base := map[string]StepEvalChapterResult{}
	for _, r := range baseline {
		base[r.Chapter] = r
	}
	var candWarns, baseWarns int
	for _, r := range candidate {
		b, ok := base[r.Chapter]
		if !ok || !b.Parsed {
			check.Detail = fmt.Sprintf("当前启用版本在「%s」上没有可对照的结果", firstNonEmptyString(r.Label, r.Chapter))
			return check
		}
		candWarns += r.Warnings
		baseWarns += b.Warnings
	}
	if len(candidate) == 0 {
		check.Detail = "没有评测章节"
		return check
	}
	limit := g.MaxWarnRatio * float64(baseWarns)
	baseValue := float64(baseWarns)
	check.Value, check.Limit, check.Baseline = float64(candWarns), limit, &baseValue
	check.Passed = float64(candWarns) <= limit+gateEpsilon
	check.Detail = fmt.Sprintf("%d 条，当前版本 %d 条，上限 %s 条（%s 倍）", candWarns, baseWarns, trimGateNumber(limit), trimGateNumber(g.MaxWarnRatio))
	return check
}

// durationCheck 是每章镜头估时合计不超过每集时长的若干倍：取各章比值的最大值；没设每集时长的章不比。
func (g StepGate) durationCheck(candidate []StepEvalChapterResult) StepGateCheck {
	check := StepGateCheck{Metric: GateDurationRatio, Label: "估时合计", Limit: g.MaxDurationRatio, Passed: true}
	worst := ""
	compared := 0
	for _, r := range candidate {
		if r.TargetSeconds <= 0 || !r.Parsed {
			continue
		}
		compared++
		ratio := r.EstSeconds / r.TargetSeconds
		if ratio > check.Value {
			check.Value = ratio
			worst = fmt.Sprintf("「%s」%s 秒 / 每集 %s 秒", firstNonEmptyString(r.Label, r.Chapter), trimGateNumber(r.EstSeconds), trimGateNumber(r.TargetSeconds))
		}
	}
	if compared == 0 {
		check.Detail = "评测章节都没设每集时长，不比"
		return check
	}
	check.Passed = check.Value <= g.MaxDurationRatio+gateEpsilon
	check.Detail = fmt.Sprintf("最长的是%s，上限 %s 倍", worst, trimGateNumber(g.MaxDurationRatio))
	return check
}

func trimGateNumber(x float64) string {
	x = math.Round(x*100) / 100
	if x == math.Trunc(x) {
		return fmt.Sprintf("%d", int64(x))
	}
	return fmt.Sprintf("%g", x)
}
