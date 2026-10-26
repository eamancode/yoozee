package drama

import (
	"strings"
	"testing"
)

// 一镜一行：每行一个镜头对象；回合标记、围栏、空行跳过；坏行计入 rejected；一行写成 {"rows": [...]} 也认。
func TestStoryboardRowsFromTextReadsOneShotPerLine(t *testing.T) {
	text := strings.Join([]string{
		`{"@turn":{"scene":"E01S01","index":1}}`,
		"```json",
		`{"plotDescription":"推门","dialogue":"林小满：七床？","spec":{"id":"E01S01-01","scene":"E01S01"}}`,
		``,
		`{"plotDescription":"空床","characters":"林小满"}`,
		`{"plotDescription":"写到一半`,
		"```",
		`{"rows":[{"plotDescription":"电梯门"},{"plotDescription":"周岸"}]}`,
	}, "\n")
	rows, stats, err := StoryboardRowsFromText(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || stats.Lines != 4 || stats.Rejected != 1 || stats.Rows != 4 || stats.Rate != 0.75 {
		t.Fatalf("解析：%d 行 %+v", len(rows), stats)
	}
	if rows[0]["shotNumber"] != 1 || rows[3]["shotNumber"] != 4 || rows[1]["durationSeconds"] != float64(6) {
		t.Fatalf("规整：%+v", rows)
	}
	if chars, ok := rows[1]["characters"].([]any); !ok || len(chars) != 0 {
		t.Fatalf("characters 不是数组时置空：%+v", rows[1]["characters"])
	}
}

// 模型没按一镜一行写、给了跨行的整段 JSON（@2 的写法）：整段兜底解析。
func TestStoryboardRowsFromTextFallsBackToWholeObject(t *testing.T) {
	text := "```json\n{\n  \"title\": \"第一集\",\n  \"rows\": [\n    {\"plotDescription\": \"推门\"},\n    {\"plotDescription\": \"空床\"}\n  ]\n}\n```"
	rows, stats, err := StoryboardRowsFromText(text)
	if err != nil || len(rows) != 2 || stats.Rate != 1 {
		t.Fatalf("整段兜底：%v %d %+v", err, len(rows), stats)
	}
	if _, _, err := StoryboardRowsFromText("抱歉，我无法完成"); err != ErrNoStoryboardRows {
		t.Fatalf("没有镜头行：%v", err)
	}
}
