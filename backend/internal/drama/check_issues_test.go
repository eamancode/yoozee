package drama

import (
	"slices"
	"testing"
)

func TestShotIssuesAnchorsLineCoverage(t *testing.T) {
	script := &Script{Scenes: []ScriptScene{
		{ID: "E01S01", Beats: []ScriptBeat{
			{ID: "E01S01L01", Kind: "line", Speaker: "林", Text: "七床的人呢？"},
			{ID: "E01S01L02", Kind: "line", Speaker: "周", Text: "刚推走。"},
			{ID: "E01S01L03", Kind: "line", Speaker: "林", Text: "推去哪了？"},
		}},
		{ID: "E01S02", Beats: []ScriptBeat{
			{ID: "E01S02L01", Kind: "line", Speaker: "周", Text: "别问了。"},
		}},
	}}
	shotlist := Shotlist{Shots: []Shot{
		{ID: "E01S01-01", Scene: "E01S01", Lines: []ShotLine{{LineID: "E01S01L01"}}},
		{ID: "E01S01-02", Scene: "E01S01", Lines: []ShotLine{{LineID: "E01S01L03"}}},
		{ID: "E01S01-03", Scene: "E01S01", Lines: []ShotLine{{LineID: "E01S01L03"}}},
		{ID: "E01S02-01", Scene: "E01S02"},
	}}
	report := CheckShots(shotlist, CheckContext{Script: script})
	issues := ShotIssues(report, shotlist, script, "")
	byLine := map[string]ShotIssue{}
	for _, issue := range issues {
		if issue.Rule == "R-SHOT-001" {
			byLine[issue.LineID] = issue
		}
	}
	// L02 没落镜：挂在前一句 L01 所在的镜头，给「补进这个镜头」。
	if got := byLine["E01S01L02"]; got.Fix != ShotIssueFixAddLine || !slices.Equal(got.Shots, []string{"E01S01-01"}) || got.Scene != "E01S01" {
		t.Fatalf("L02 没落镜的挂法不对：%+v", got)
	}
	// L03 落在两镜：两镜都挂，给「只保留在这个镜头」。
	if got := byLine["E01S01L03"]; got.Fix != ShotIssueFixKeepLine || !slices.Equal(got.Shots, []string{"E01S01-02", "E01S01-03"}) {
		t.Fatalf("L03 落在两镜的挂法不对：%+v", got)
	}
	// 第 2 场一句都没落镜：挂在本场第一镜。
	if got := byLine["E01S02L01"]; got.Fix != ShotIssueFixAddLine || !slices.Equal(got.Shots, []string{"E01S02-01"}) || got.Scene != "E01S02" {
		t.Fatalf("E01S02L01 的挂法不对：%+v", got)
	}
	// 只取 error 时 warn 不进来。
	for _, issue := range ShotIssues(report, shotlist, script, "error") {
		if issue.Level != "error" {
			t.Fatalf("只取 error 却拿到 %+v", issue)
		}
	}
}
