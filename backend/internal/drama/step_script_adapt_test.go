package drama

import (
	"html"
	"regexp"
	"strings"
	"testing"
)

const adaptSampleOutput = `{"@turn": {"index": 1, "total": 2, "scene": "1", "heading": "第 1 集", "attempt": 1}}
{"no": 1, "title": "七床空了", "sources": [{"unitId": "c1", "from": 1, "to": 8}]}
## 内·病房走廊·夜
（夜里的病房走廊，日光灯一盏接一盏。）
林小满：七床？
林小满（压低声音）：七床的人呢？
## 内·电梯口·夜
周岸：小满。
{"@turn": {"index": 2, "total": 2, "scene": "2", "heading": "第 2 集", "attempt": 1}}
{"no": 2, "title": "现在信了", "sources": [{"unitId": "c1", "from": 9, "to": 20}, {"unitId": "c2", "from": 1, "to": 3}]}
## 内·电梯口·夜
（阿坤从楼梯间冲出来。）
周岸：退后！
`

func TestScriptAdaptContractText(t *testing.T) {
	c, err := BuiltinContract(ContractScriptAdapt)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Blocks() || c.Lines() {
		t.Fatal("剧本优化契约应是一集一块")
	}
	text := c.Text(c.DefaultRequirement(), ContractTextOptions{SkillShaped: true})
	for _, want := range []string{"【受保护输出契约】", "至少输出 1 项", `{"no": 1`, "## 内·病房走廊·夜", "人物：台词", "sources（对象数组", "text（本集剧本正文"} {
		if !strings.Contains(text, want) {
			t.Fatalf("契约文字缺「%s」：\n%s", want, text)
		}
	}
}

func TestScriptAdaptEpisodesFromText(t *testing.T) {
	episodes, stats := ScriptAdaptEpisodesFromText(adaptSampleOutput)
	if len(episodes) != 2 || stats.Blocks != 2 || stats.Episodes != 2 || stats.Rate != 1 {
		t.Fatalf("episodes = %+v stats = %+v", episodes, stats)
	}
	first := episodes[0]
	if first.No != 1 || first.Title != "七床空了" || len(first.Scenes) != 2 || first.Scenes[1].Heading != "内·电梯口·夜" || len(first.Sources) != 1 || first.Sources[0] != (AdaptSource{UnitID: "c1", From: 1, To: 8}) {
		t.Fatalf("第 1 集 = %+v", first)
	}
	if strings.Contains(first.Text, "@turn") || strings.Contains(first.Text, `"no"`) {
		t.Fatalf("正文不该带回合标记或 JSON 头：%s", first.Text)
	}
	if got := MainAdaptSource(episodes[1].Sources); got != "c1" {
		t.Fatalf("主要来源 = %s", got)
	}
}

// 技能原生的「【场景1】地点｜内｜夜」「【画面】」写法、人物名外的 ** 规整成契约格式；头之前的「第X集：标题」当标题。
func TestScriptAdaptNormalizesSkillFormat(t *testing.T) {
	text := "{\"no\": 1, \"sources\": [{\"unitId\": \"c1\", \"from\": 1, \"to\": 2}]}\n第1集：雨夜来信\n【场景1】病房走廊｜内｜夜\n【人物】林小满\n【画面】走廊尽头的灯在闪。\n**林小满**：七床？\n【结尾钩子】门后有人。\n"
	episodes, _ := ScriptAdaptEpisodesFromText(text)
	if len(episodes) != 1 {
		t.Fatalf("episodes = %+v", episodes)
	}
	ep := episodes[0]
	if ep.Title != "雨夜来信" || len(ep.Scenes) != 1 || ep.Scenes[0].Heading != "内·病房走廊·夜" {
		t.Fatalf("ep = %+v", ep)
	}
	for _, want := range []string{"## 内·病房走廊·夜", "（走廊尽头的灯在闪。）", "林小满：七床？", "（结尾钩子：门后有人。）"} {
		if !strings.Contains(ep.Text, want) {
			t.Fatalf("正文缺「%s」：\n%s", want, ep.Text)
		}
	}
	// 没写 JSON 头、只有正文时按一集算。
	loose, _ := ScriptAdaptEpisodesFromText("好的，下面是剧本：\n## 外·集市·夜\n苏迟：天的拉链开了。\n")
	if len(loose) != 1 || loose[0].Header || len(loose[0].Scenes) != 1 {
		t.Fatalf("loose = %+v", loose)
	}
}

func TestCountScriptAdaptTurn(t *testing.T) {
	one := "{\"no\": 1, \"title\": \"a\", \"sources\": [{\"unitId\": \"c1\", \"from\": 1, \"to\": 2}]}\n## 内·客厅·夜\n甲：你好。\n"
	if n, err := CountScriptAdaptTurn(one, []string{"c1"}); err != nil || n != 1 {
		t.Fatalf("n = %d err = %v", n, err)
	}
	for name, c := range map[string]struct {
		text    string
		sources []string
	}{
		"两集":    {text: one + strings.Replace(one, `"no": 1`, `"no": 2`, 1), sources: []string{"c1"}},
		"没有场次头": {text: "{\"no\": 1, \"sources\": [{\"unitId\": \"c1\", \"from\": 1, \"to\": 2}]}\n甲：你好。\n", sources: []string{"c1"}},
		"来源不对":  {text: one, sources: []string{"c2"}},
		"没写来源":  {text: "{\"no\": 1, \"title\": \"a\"}\n## 内·客厅·夜\n甲：你好。\n", sources: []string{"c1"}},
	} {
		if _, err := CountScriptAdaptTurn(c.text, c.sources); err == nil {
			t.Fatalf("%s 应当重试", name)
		}
	}
	// 调节奏不校验来源。
	if _, err := CountScriptAdaptTurn("{\"no\": 1, \"title\": \"a\", \"sources\": []}\n## 内·客厅·夜\n甲：你好。\n", nil); err != nil {
		t.Fatal(err)
	}
}

func TestCheckScriptAdapt(t *testing.T) {
	episodes, _ := ScriptAdaptEpisodesFromText(adaptSampleOutput)
	ctx := ScriptAdaptCheckContext{Mode: ScriptAdaptModeAdapt, Paragraphs: map[string]int{"c1": 20, "c2": 3}, Episodes: 2}
	if r := CheckScriptAdapt(episodes, ctx); r.Errors != 0 || r.Warnings != 0 {
		t.Fatalf("report = %+v", r)
	}
	ctx.Paragraphs = map[string]int{"c1": 10}
	r := CheckScriptAdapt(episodes, ctx)
	rules := map[string]int{}
	for _, item := range r.Items {
		rules[item.Rule]++
	}
	if r.Errors != 1 || rules[RuleAdaptUnknownUnit] != 1 || rules[RuleAdaptRange] != 1 {
		t.Fatalf("report = %+v", r)
	}
	bad, _ := ScriptAdaptEpisodesFromText("{\"no\": 1, \"title\": \"a\", \"sources\": [{\"unitId\": \"c1\", \"from\": 1, \"to\": 2}]}\n## 内·客厅·夜\n一个穿黑衣服的陌生男人：你好。\n【张三】：走。\n（门开了。）\n")
	r = CheckScriptAdapt(bad, ScriptAdaptCheckContext{Mode: ScriptAdaptModeAdapt, Paragraphs: map[string]int{"c1": 2}})
	rules = map[string]int{}
	for _, item := range r.Items {
		rules[item.Rule]++
	}
	if r.Errors != 0 || rules[RuleAdaptLine] != 2 {
		t.Fatalf("台词行格式应提醒两条：%+v", r)
	}
	if r := CheckScriptAdapt(nil, ScriptAdaptCheckContext{}); r.Errors != 1 || r.Items[0].Rule != RuleAdaptNoEpisodes {
		t.Fatalf("report = %+v", r)
	}
}

// 剧集正文写成 episode 单元后，按章节正文的同一条路径（去标签逐行）能切出场、读出台词；来源注释能读回来、能换掉。
func TestEpisodeSourceTextRoundTrip(t *testing.T) {
	episodes, _ := ScriptAdaptEpisodesFromText(adaptSampleOutput)
	ep := episodes[1]
	stored := EpisodeSourceText(ep.Sources, ep.Text+"\n张三：<你>好 & 再见")
	if !strings.HasPrefix(stored, "<!-- sources: c1:9-20;c2:1-3 -->") {
		t.Fatalf("stored = %s", stored)
	}
	if got := EpisodeSources(stored); len(got) != 2 || got[1] != (AdaptSource{UnitID: "c2", From: 1, To: 3}) {
		t.Fatalf("sources = %+v", got)
	}
	plain := html.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(strings.ReplaceAll(stored, "</p>", "\n"), ""))
	script := ScriptFromText(plain, "现在信了")
	if len(script.Scenes) != 1 || script.Scenes[0].Heading != "内·电梯口·夜" || script.LineCount() != 2 {
		t.Fatalf("script = %+v", script)
	}
	if last := script.Scenes[0].Beats[len(script.Scenes[0].Beats)-1]; last.Speaker != "张三" || last.Text != "<你>好 & 再见" {
		t.Fatalf("last = %+v", last)
	}
	replaced := WithEpisodeSources(stored, []AdaptSource{{UnitID: "c9", From: 1, To: 1}})
	if !strings.HasPrefix(replaced, "<!-- sources: c9:1-1 --><p>") || strings.Count(replaced, "<!--") != 1 {
		t.Fatalf("replaced = %s", replaced)
	}
	if got := WithEpisodeSources(stored, nil); strings.Contains(got, "<!--") || EpisodeSources(got) != nil {
		t.Fatalf("去掉注释 = %s", got)
	}
}

func TestScriptAdaptEpisodeCountAndPrompts(t *testing.T) {
	sources := []ScriptAdaptSource{{UnitID: "c1", Title: "第一章", Text: strings.Repeat("字", 3240)}, {UnitID: "c2", Title: "第二章", Text: "第一段\n\n第二段"}}
	if n := ScriptAdaptEpisodeCount(ScriptAdaptModeAdapt, sources, 0, 0); n != 2 {
		t.Fatalf("一章一集：%d", n)
	}
	if n := ScriptAdaptEpisodeCount(ScriptAdaptModeAdapt, sources, 5, 90); n != 5 {
		t.Fatalf("给了集数用集数：%d", n)
	}
	if n := ScriptAdaptEpisodeCount(ScriptAdaptModeAdapt, sources, 0, 60); n != 3 {
		t.Fatalf("3246 字 / (60 秒 × 18) ≈ 3 集：%d", n)
	}
	if n := ScriptAdaptEpisodeCount(ScriptAdaptModeRhythm, sources, 9, 0); n != 2 {
		t.Fatalf("调节奏集数不变：%d", n)
	}
	task := ScriptAdaptTask(ScriptAdaptModeAdapt, sources, 2)
	if !strings.Contains(task, "【原文章节 2】第二章（unitId: c2，共 2 段）\n[1] 第一段\n[2] 第二段") {
		t.Fatalf("task = %s", task)
	}
	turns := ScriptAdaptTurns(ScriptAdaptModeAdapt, sources, 2)
	if len(turns) != 2 || turns[1].ID != "2" || turns[1].Heading != "第 2 集" {
		t.Fatalf("turns = %+v", turns)
	}
	if intro := ScriptAdaptTurnPlan(turns); !strings.Contains(intro, "共 2 集") || strings.Contains(intro, "对应【剧集") {
		t.Fatalf("intro = %s", intro)
	}
	if next := ScriptAdaptTurnPrompt(turns, 2); !strings.Contains(next, "继续写第 2 集") || !strings.Contains(next, "接着第 1 集") {
		t.Fatalf("next = %s", next)
	}
	rhythm := ScriptAdaptTurns(ScriptAdaptModeRhythm, []ScriptAdaptSource{{UnitID: "e1", Title: "雨夜"}, {UnitID: "e2", Title: "归城"}}, 2)
	if next := ScriptAdaptTurnPrompt(rhythm, 2); !strings.Contains(next, "对应【剧集 2】「归城」") {
		t.Fatalf("rhythm next = %s", next)
	}
	brief := StepBriefPrompt(`{"title": "七床", "episode_seconds": 90, "segment_seconds": 15, "constraints": ["不出现血腥画面"], "purpose": {"value": "抖音投放", "status": "已确认"}}`)
	for _, want := range []string{"片名：七床", "用途：抖音投放", "每集时长：90 秒", "单段最长：15 秒", "约束：不出现血腥画面"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("brief 缺「%s」：%s", want, brief)
		}
	}
	if StepBriefPrompt("") != "" || StepBriefPrompt("{坏的") != "" {
		t.Fatal("需求文档为空或读不出时不写")
	}
}

func TestStepGateMetrics(t *testing.T) {
	step, _ := StepByID(StepScriptAdapt)
	verdict := step.Gate.Evaluate([]StepEvalChapterResult{{Chapter: "a", Parsed: true, Errors: 3}, {Chapter: "b", Parsed: true}}, nil)
	if !verdict.Passed || len(verdict.Checks) != 1 || verdict.Checks[0].Metric != GateParseRate {
		t.Fatalf("剧本优化只判解析率：%+v", verdict)
	}
	if v := step.Gate.Evaluate([]StepEvalChapterResult{{Chapter: "a", Parsed: true}, {Chapter: "b"}}, nil); v.Passed {
		t.Fatalf("解析率 0.5 < 0.9 不过：%+v", v)
	}
}

func TestDeriveScriptEndStateByEpisodes(t *testing.T) {
	script := func(in ProgressInput) EndState {
		for _, e := range Derive(in).EndStates {
			if e.Key == "script" {
				return e
			}
		}
		t.Fatal("没有「剧本定了」")
		return EndState{}
	}
	if e := script(ProgressInput{}); e.Done || !strings.Contains(e.Detail, "还没有剧集") || e.Skill != "yz-script-adapt" {
		t.Fatalf("e = %+v", e)
	}
	if e := script(ProgressInput{Episodes: []EpisodeInput{{ID: "e1", Title: "一", WordCount: 10}, {ID: "e2", Title: "二"}}}); e.Done || !strings.Contains(e.Detail, "第 2 集「二」") {
		t.Fatalf("e = %+v", e)
	}
	if e := script(ProgressInput{Episodes: []EpisodeInput{{ID: "e1", Title: "一", WordCount: 10}}}); !e.Done || e.Detail != "1 集剧本" {
		t.Fatalf("e = %+v", e)
	}
}
