package drama

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builtinAssets "yingce/backend/builtin"
)

// 与 yoozee-skills/tests/test_compile.py 共用样例和期望结果：两边编译必须逐字一致。
var compileCases = [][2]string{
	{"segment-dialogue", "seedance-2.0"}, {"segment-dialogue", "seedance-2.5"}, {"segment-dialogue", "minimax-h3"},
	{"segment-long", "seedance-2.5"}, {"segment-long", "seedance-2.0"}, {"segment-long", "minimax-h3"},
	{"segment-firstframe", "seedance-2.0"}, {"segment-firstframe", "minimax-h3"},
}

const skillsTestDir = "../../../yoozee-skills/tests"

func renderCompileResult(r CompileResult) string {
	files := make([]string, 0, len(r.Files))
	for _, f := range r.Files {
		files = append(files, fmt.Sprintf("%s\t%s\t%s", f.Tag, f.File, f.Role))
	}
	issues := make([]string, 0, len(r.Issues))
	for _, i := range r.Issues {
		issues = append(issues, fmt.Sprintf("%s: %s", i.Level, i.Message))
	}
	return fmt.Sprintf("model: %s\nduration: %d\n--- prompt\n%s\n--- files\n%s\n--- issues\n%s\n", r.Model, r.Duration, r.Prompt, strings.Join(files, "\n"), strings.Join(issues, "\n"))
}

func TestCompileMatchesSkillPackageGoldens(t *testing.T) {
	for _, c := range compileCases {
		raw, err := os.ReadFile(filepath.Join(skillsTestDir, "fixtures", c[0]+".json"))
		if err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		seg, err := ParseSegment(raw)
		if err != nil {
			t.Fatalf("%s: %v", c[0], err)
		}
		m, ok := ModelByID(c[1])
		if !ok {
			t.Fatalf("model %s missing", c[1])
		}
		result, err := Compile(seg, m, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		want, err := os.ReadFile(filepath.Join(skillsTestDir, "expected", c[0]+"."+c[1]+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if got := renderCompileResult(result); got != string(want) {
			t.Errorf("%s × %s 与技能包的编译结果不一致\n--- got\n%s\n--- want\n%s", c[0], c[1], got, want)
		}
	}
}

func TestBuiltinModelsMatchSkillPackage(t *testing.T) {
	builtin, err := builtinAssets.FS.Open(modelsPath)
	if err != nil {
		t.Fatal(err)
	}
	defer builtin.Close()
	var a, b bytes.Buffer
	if _, err := a.ReadFrom(builtin); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("../../../yoozee-skills/kb/kb-dialects/models.json")
	if err != nil {
		t.Fatal(err)
	}
	b.Write(src)
	if a.String() != b.String() {
		t.Fatal("backend/builtin/kb-dialects/models.json 与 yoozee-skills 源文件不一致：运行 python3 yoozee-skills/build.py")
	}
}

func TestSegmentCapsFollowModel(t *testing.T) {
	cases := map[string]int{"seedance-2.0": 15, "minimax-h3": 15, "seedance-2.5": 30}
	for id, want := range cases {
		m, ok := ModelByID(id)
		if !ok || m.Duration.Max != want {
			t.Fatalf("%s 一段上限应为 %d 秒，得到 %+v", id, want, m.Duration)
		}
	}
	reg, err := Models()
	if err != nil || reg.Default != "seedance-2.0" {
		t.Fatalf("默认模型应为 seedance-2.0：%v %v", reg.Default, err)
	}
}

func TestMatchChannelModelPrefersLongestFragment(t *testing.T) {
	for name, want := range map[string]string{
		"doubao-seedance-2-0-260128": "seedance-2.0",
		"doubao-seedance-2-5-pro":    "seedance-2.5",
		"MiniMax-H3":                 "minimax-h3",
	} {
		m, ok := MatchChannelModel(name)
		if !ok || m.ID != want {
			t.Errorf("%s → %s, got %v %v", name, want, m.ID, ok)
		}
	}
	if _, ok := MatchChannelModel("kling-v2"); ok {
		t.Error("kling 不应匹配任何模型族")
	}
}

func TestParseSegmentRejectsUnknownFieldsAndBadIDs(t *testing.T) {
	if _, err := ParseSegment([]byte(`{"id":"S01","scene":"一","dur":5,"mode":"lock_identity","shots":[{"at":0,"text":"x"}],"extra":1}`)); err == nil {
		t.Error("未知字段应报错")
	}
	if _, err := ParseSegment([]byte(`{"id":"shot-1","scene":"一","dur":5,"mode":"lock_identity","shots":[{"at":0,"text":"x"}]}`)); err == nil {
		t.Error("非法段号应报错")
	}
	var seg Segment
	if err := json.Unmarshal([]byte(`{"id":"S01","scene":"一","dur":5,"mode":"text_only","shots":[{"at":0,"text":"x","lines":[{"speaker":"A","text":"你好"}]}]}`), &seg); err != nil || seg.Dialogue() != "你好" {
		t.Errorf("台词拼接错误: %v %q", err, seg.Dialogue())
	}
}
