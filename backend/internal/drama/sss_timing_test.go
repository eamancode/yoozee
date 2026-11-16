package drama

import (
	"strings"
	"testing"
)

const sssTimingTable = "#### 镜头表\n\n| 镜 | 时间 | 时长 | 景别·视场角 | 机位 | 运动 | 焦点 | 画面与表演 | 台词/声音 | 职责 |\n|---|---|---|---|---|---|---|---|---|---|\n"

func sssTimingCard(rows string) SSSUnitCard {
	return SSSUnitCard{ID: "E1-U2", TotalSeconds: 8, Shots: 2, Freeze: true, Body: "### E1-U2｜短档｜叙事 7 秒＋定格 1 秒＝8 秒｜2 镜＋定格｜雨棚（本场第 1 段／共 2 段）｜施压\n\n" + sssTimingTable + rows}
}

// 线上短剧样本（第 5 轮）：标题「叙事 7 秒＋定格 1 秒＝8 秒」，镜头表只排到 7 秒、没写定格那一行。校验照旧拒绝（不吞 1 秒），
// 错误说清缺的是全景定格那一行（重试时作为【本轮输出修正】回给模型）；补上定格行就通过；不带定格的单元仍按结束秒对总时长。
func TestSSSCardTimingTailRow(t *testing.T) {
	missing := sssTimingCard("| 1 | 0–3 | 3 | 近景 | 平视 | 固定 | 眼睛 | 阿婆递橙汁。 | 阿婆：喝口热的。 | 说 |\n| 2 | 3–7 | 4 | 近景 | 平视 | 固定 | 手 | 林深接过。 | —— | 反应 |\n")
	err := ValidateSSSCardBody(missing, "E1-U2", SSSUnitLimits{})
	if err == nil || !strings.Contains(err.Error(), "最后一镜在 7 秒结束，标题行的总时长是 8 秒") || !strings.Contains(err.Error(), "全景定格那一行") || !strings.Contains(err.Error(), "在 8 秒结束") {
		t.Fatalf("missing freeze row must be rejected with a precise hint: %v", err)
	}
	withFreeze := sssTimingCard("| 1 | 0–3 | 3 | 近景 | 平视 | 固定 | 眼睛 | 阿婆递橙汁。 | 阿婆：喝口热的。 | 说 |\n| 2 | 3–7 | 4 | 近景 | 平视 | 固定 | 手 | 林深接过。 | —— | 反应 |\n| 3 | 7–8 | 1 | 全景 84° | 高位斜俯 | 固定 | 全景深 | 二人保持最后状态。 | —— | 全景定格 |\n")
	if err := ValidateSSSCardBody(withFreeze, "E1-U2", SSSUnitLimits{}); err != nil {
		t.Fatalf("freeze row listed in the table must pass: %v", err)
	}
	// 写了定格行、时间却还是对不上：不是缺行，按普通的结束秒提示，仍拒绝。
	short := sssTimingCard("| 1 | 0–3 | 3 | 近景 | 平视 | 固定 | 眼睛 | 阿婆递橙汁。 | 阿婆：喝口热的。 | 说 |\n| 2 | 3–6 | 3 | 近景 | 平视 | 固定 | 手 | 林深接过。 | —— | 反应 |\n| 3 | 6–7 | 1 | 全景 | 高位 | 固定 | 全景深 | 定格。 | —— | 全景定格 |\n")
	if err := ValidateSSSCardBody(short, "E1-U2", SSSUnitLimits{}); err == nil || strings.Contains(err.Error(), "全景定格那一行") || !strings.Contains(err.Error(), "技术尾巴也写进镜头表") {
		t.Fatalf("mismatched total with a freeze row: %v", err)
	}
}

// 单元卡轮 / 返修轮的运行时契约（不改技能原文）：本轮任务写明定格和技术尾巴进镜头表、最后一行等于总时长；
// 另附单元总表里这一单元的原行做时长核算，找不到时不附。
func TestSSSCardTurnCarriesTimingContract(t *testing.T) {
	for _, kind := range []string{SSSTurnCard, SSSTurnRepair} {
		instruction := SSSTurnInstruction(kind, "E1-U2", []string{"A01"})
		if !strings.Contains(instruction, "镜头表最后一行就是那一秒全景定格") || !strings.Contains(instruction, "最后一行的结束秒必须等于标题的总时长") {
			t.Fatalf("%s instruction lacks the timing contract: %s", kind, instruction)
		}
	}
	cut := "## 整场镜头表\n\n镜头……\n\n## 第四部分　单元总表\n\n| 单元 | 档 | 叙事时长 | 总时长 | 镜数 |\n|---|---|---|---|---|\n| E1-U1 | 短 | 13 秒 | 14 秒 | 4 |\n| E1-U2 | 短 | 7 秒 | 8 秒 | 2 |\n\n## 启发库选用\n\n| 单元 | 库号 |\n|---|---|\n| E1-U1 | A01 |\n| E1-U2 | C01 |\n"
	line := SSSUnitTimingLine(cut, "E1-U2")
	if !strings.Contains(line, "| 单元 | 档 | 叙事时长 | 总时长 | 镜数 |") || !strings.Contains(line, "| E1-U2 | 短 | 7 秒 | 8 秒 | 2 |") || strings.Contains(line, "E1-U1") || !strings.Contains(line, "定格那一秒是镜头表的最后一行") {
		t.Fatalf("timing line = %q", line)
	}
	if SSSUnitTimingLine(cut, "E1-U9") != "" || SSSUnitTimingLine("没有单元总表", "E1-U2") != "" {
		t.Fatal("unknown unit or missing table must not add a timing line")
	}
}
