package drama

import (
	"strings"
	"testing"
)

const sssFixtureEpisode = "{\"no\":1,\"title\":\"车站\",\"sources\":[{\"unitId\":\"c1\",\"from\":1,\"to\":1}]}\n## 外·站台·夜\n阿婆：你说“等我”。\n## 第一部分　本集设计\n车站重逢。\n## 第二部分 台词定稿表\n| 序 | 定稿台词 | 处理 |\n|---|---|---|\n| 1 | 你说\"等 我\"。 | 保留 |\n| 2 | （删） | 删 |"

func TestSSSScriptEpisodeContract(t *testing.T) {
	turn := TurnContext{Contract: ContractSSSScriptAdapt, Sources: []string{"c1"}}
	if n, err := CountScriptAdaptRun(sssFixtureEpisode, turn); err != nil || n != 1 {
		t.Fatalf("合格输出 %d %v", n, err)
	}
	for _, bad := range []string{strings.Replace(sssFixtureEpisode, "车站重逢。", "", 1), strings.Replace(sssFixtureEpisode, "你说\"等 我\"。", "不存在的台词", 1), strings.Replace(sssFixtureEpisode, "## 第一部分　本集设计", "## 其他", 1), strings.SplitN(sssFixtureEpisode, "\n", 2)[1]} {
		if _, err := CountScriptAdaptRun(bad, turn); err == nil {
			t.Fatalf("接受了无效回合 %q", bad)
		}
	}
	text := "## 立意页\n重逢\n## 全集结构表\n三集\n" + strings.Repeat(sssFixtureEpisode+"\n", 3)
	episodes, stats := ScriptAdaptEpisodesFromText(text, ContractSSSScriptAdapt)
	if len(episodes) != 3 || stats.Episodes != 3 {
		t.Fatal(stats)
	}
	for _, ep := range episodes {
		if len(ep.Scenes) != 1 || strings.Contains(ep.Text, "本集设计") || !strings.Contains(ep.Design, "定稿台词") {
			t.Fatalf("%+v", ep)
		}
	}
}

func TestSSSMarkedDialogue(t *testing.T) {
	turn := TurnContext{Contract: ContractSSSScriptAdapt, Sources: []string{"c1"}, LockedLines: SSSLockedLines([]ScriptAdaptSource{{UnitID: "c1", Text: "【保留】阿婆：你说“等我”。"}})}
	if n, err := CountScriptAdaptRun(sssFixtureEpisode, turn); n != 1 || err != nil {
		t.Fatal(n, err)
	}
	changed := strings.NewReplacer("你说“等我”。", "等一会儿。", "你说\"等 我\"。", "等一会儿。").Replace(sssFixtureEpisode)
	if _, err := CountScriptAdaptRun(changed, turn); err == nil || !strings.Contains(err.Error(), "逐字保留") {
		t.Fatal("剧本和表一起改动仍需验证锁定原句", err)
	}
}
