package drama

import (
	"embed"
	"fmt"
	"path"
)

// 评测章节的数据：每章一个目录 evalset/<id>/，放项目、章节、资产清单三份接口 JSON（project.json、unit.json、assets.json），
// 与浏览器从接口拿到的同一形状。由 app 测试按真实用户路径（建项目、粘贴正文、提取资产并确认）建出后导出：
// go test ./internal/app -run TestDramaEvalSetFixtures -update-drama-evalset

//go:embed evalset
var evalSetFS embed.FS

// EvalChapterFiles 是一个评测章节的三份接口 JSON。
type EvalChapterFiles struct {
	Project []byte
	Unit    []byte
	Assets  []byte
}

// ReadEvalChapter 读一个评测章节的数据。
func ReadEvalChapter(id string) (EvalChapterFiles, error) {
	var out EvalChapterFiles
	for name, into := range map[string]*[]byte{"project.json": &out.Project, "unit.json": &out.Unit, "assets.json": &out.Assets} {
		raw, err := evalSetFS.ReadFile(path.Join("evalset", id, name))
		if err != nil {
			return EvalChapterFiles{}, fmt.Errorf("评测章节 %s 缺少 %s：%w", id, name, err)
		}
		*into = raw
	}
	return out, nil
}
