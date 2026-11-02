package app

import (
	"testing"

	"yingce/backend/internal/drama"
)

func TestResumeSceneIdentityUsesOriginalMergedHeadings(t *testing.T) {
	previous := []drama.ShotsScene{{ID: "E01S01", Heading: "果汁摊-日-内", Opening: "摊主打开机器。", SourceHeadings: []string{"第四场｜果汁摊-日-内", "第五场｜果汁摊-日-内"}}}
	current := []drama.ShotsScene{{ID: "E01S01", Heading: "内·果汁摊·日", Opening: previous[0].Opening, SourceHeadings: append([]string(nil), previous[0].SourceHeadings...)}}
	if !sameShotsScenes(previous, current) {
		t.Fatal("只修正合并场展示名，不应丢弃原文未变的检查点")
	}
	current[0].SourceHeadings[1] = "第五场｜果汁摊-夜-内"
	if sameShotsScenes(previous, current) {
		t.Fatal("原始场次头已改，必须重新生成")
	}
	current[0].SourceHeadings = append([]string(nil), previous[0].SourceHeadings...)
	current[0].Opening = "摊主关闭机器。"
	if sameShotsScenes(previous, current) {
		t.Fatal("正文锚已改，必须重新生成")
	}
	previous[0].SourceHeadings, current[0].SourceHeadings = nil, nil
	current[0].Opening = previous[0].Opening
	if sameShotsScenes(previous, current) {
		t.Fatal("没有原始合并场次头时，不能忽略标题变化")
	}
}
