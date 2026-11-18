package drama

import (
	"strings"
	"testing"
)

func TestSceneReferencesRemoveOnlyTheRequestedDeclaration(t *testing.T) {
	prompt := "12秒。素材映射：@图3 为当铺；@图1 为林夏。镜头从门口推进。\n生效参考素材\n@图3：对照旧室内布局\n@图2：钥匙\n声音\n保留声音正文"
	result := StripSceneDeclaration(prompt, "@图3")
	if strings.Contains(result, "@图3") || !strings.Contains(result, "12秒。素材映射：@图1 为林夏。镜头从门口推进。") ||
		!strings.Contains(result, "@图2：钥匙\n声音\n保留声音正文") {
		t.Fatalf("unsafe removal: %q", result)
	}
}

func TestSceneBindingConflictsDoNotConfuseWeatherWithAConfirmedMissingAnchor(t *testing.T) {
	seg := Segment{Verbatim: "场景背景\n黄昏，室内灯光变化。\n首帧与走位\n无风时，演员推开木门。"}
	space := SceneSpace{Facts: &SceneFacts{InOut: "内", Absent: []string{"木门"}}}
	issues := SceneConflicts(seg, space)
	if len(issues) != 1 || !strings.Contains(issues[0], "演员推开木门") {
		t.Fatalf("incorrect conflicts: %+v", issues)
	}
}
