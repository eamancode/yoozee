package app

import (
	"slices"
	"testing"

	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// 卡 G：引用资产的镜头按项目编号列（第 2 集写 E02S01-P1），能看出是哪一集的哪一节；存下的仍是单元内编号。
func TestAssetShotReferenceLabelsUseProjectCodes(t *testing.T) {
	units := []model.ProjectUnit{
		{ID: "chapter", Kind: model.ProjectUnitKindChapter, Title: "第一章", Position: 0},
		{ID: "ep2", Kind: model.ProjectUnitKindEpisode, Title: "第二集", Position: 2},
		{ID: "ep1", Kind: model.ProjectUnitKindEpisode, Title: "第一集", Position: 1},
	}
	refs := []repository.AssetShotReference{
		{ShotID: "a", UnitID: "ep2", Position: 1, Title: "S02 夺刀", SpecJSON: `{"id":"S02","scene":"E01S01","section":"E01S01-P2"}`},
		{ShotID: "b", UnitID: "ep1", Position: 0, Title: "S01 推门", SpecJSON: `{"id":"S01","scene":"E01S01","section":"E01S01-P1"}`},
		{ShotID: "c", UnitID: "ep2", Position: 0, Title: "S01 护士站", SpecJSON: `{"id":"S01","scene":"E01S01"}`},
		{ShotID: "d", UnitID: "chapter", Position: 0, Title: "S01 原文", SpecJSON: `{"id":"S01","scene":"E01S01","section":"E01S01-P1"}`},
		{ShotID: "e", UnitID: "ep2", Position: 5, Title: "手写镜头"},
	}
	got := assetShotReferenceLabels(refs, units)
	want := []string{"第一章 S01 原文", "E01S01-P1（S01）", "E02S01 · S01", "E02S01-P2（S02）", "第二集 手写镜头"}
	if !slices.Equal(got, want) {
		t.Fatalf("引用列表：%q，想要 %q", got, want)
	}
	for _, tc := range []struct {
		code    string
		episode int
		want    string
	}{{"E01S03-P1", 2, "E02S03-P1"}, {"E01S03", 12, "E12S03"}, {"E01S03", 1, "E01S03"}, {"E02S03", 3, "E02S03"}} {
		if got := projectSceneCode(tc.code, tc.episode); got != tc.want {
			t.Fatalf("projectSceneCode(%s, %d) = %s，想要 %s", tc.code, tc.episode, got, tc.want)
		}
	}
}
