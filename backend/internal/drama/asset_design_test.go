package drama

import "testing"

func TestAssetDesignPreservesAttachmentEvidenceAcrossScenes(t *testing.T) {
	text := `{"category":"prop","name":"旧保温杯","appearance":"杯盖裂缝和胶带残印","prompt_en":"old flask","first_scene":1,"design":{"characters":["阿婆"],"evidence":[{"kind":"原文","location":"第一场","text":"杯盖裂了一道缝"}],"states":[{"location":"第一场","trigger":"赠杯","state":"掉漆、裂盖"}],"prop_design":"金属杯身，故事信物，优先制作"}}
{"category":"prop","name":"旧保温杯","appearance":"","prompt_en":"","first_scene":6,"design":{"characters":["林深"],"evidence":[{"kind":"原文","location":"第六场","text":"天天摸。五年了。"}],"states":[{"location":"第六场","trigger":"五年摩挲","state":"一小块金属磨亮"}]}}`
	rows, stats := AssetsExtractFromText(text)
	if stats.Invalid != 0 || len(rows) != 2 {
		t.Fatalf("parse: %+v", stats)
	}
	checked := CheckAssetsExtract(rows, AssetsExtractCheckContext{Scenes: 8})
	if len(checked.Rows) != 1 {
		t.Fatalf("merge: %+v", checked)
	}
	r := checked.Rows[0]
	if r.Appearance != "杯盖裂缝和胶带残印" || len(r.Design.States) != 2 || len(r.Design.Evidence) != 2 || len(r.Design.Characters) != 2 {
		t.Fatalf("attachment design lost: %+v", r)
	}
	merged := MergeAssetDesign(r.Design, rows[1].Design)
	if len(merged.States) != 2 || len(merged.Evidence) != 2 {
		t.Fatalf("retry duplicated evidence: %+v", merged)
	}
}
