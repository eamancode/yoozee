// drama-eval 是离线评测重跑（M5）：用夹具目录里已存的分镜行，在基线和候选两份编译档案下各跑一遍
// 「检查 → 装段 → 编译」，输出 Markdown 指标表。不连数据库、不调模型；线上项目用后台的 POST /api/admin/drama/eval。
//
//	go run ./cmd/drama-eval -fixture internal/drama/testdata/online-qichuang-on3 \
//	  -chapter internal/drama/testdata/online-qichuang/chapter.md -model seedance-2.5 -candidate candidate.json [-imaged all]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func main() {
	fixture := flag.String("fixture", "", "夹具目录：rows.json（{\"rows\": [...]} 或分镜行数组）、可选 assets.json、chapter.md")
	chapter := flag.String("chapter", "", "章节正文（纯文本 / Markdown）；不给时读夹具目录的 chapter.md")
	title := flag.String("title", "第一章", "章节标题")
	modelID := flag.String("model", "seedance-2.5", "生成模型（kb-dialects/models.json 里的 id）")
	candidatePath := flag.String("candidate", "", "候选编译档案 JSON（只写要改的项）；不给时为内置默认")
	baselinePath := flag.String("baseline", "", "基线编译档案 JSON；不给时为内置默认")
	jsonOut := flag.String("json", "", "把完整结果（含逐段提示词）写到这个 JSON 文件")
	imaged := flag.String("imaged", "", "哪些资产有图（能挂参考）：all、逗号分隔的资产 id；不给表示都没图（人物写外貌声明，场景、道具写进镜头描述）")
	flag.Parse()
	if err := run(*fixture, *chapter, *title, *modelID, *candidatePath, *baselinePath, *jsonOut, *imaged); err != nil {
		fmt.Fprintln(os.Stderr, "drama-eval:", err)
		os.Exit(1)
	}
}

func run(fixture, chapter, title, modelID, candidatePath, baselinePath, jsonOut, imagedFlag string) error {
	if fixture == "" {
		return fmt.Errorf("需要 -fixture 目录")
	}
	rows, err := readRows(filepath.Join(fixture, "rows.json"))
	if err != nil {
		return err
	}
	assets, err := readAssets(filepath.Join(fixture, "assets.json"))
	if err != nil {
		return err
	}
	if chapter == "" {
		chapter = filepath.Join(fixture, "chapter.md")
	}
	text, err := os.ReadFile(chapter)
	if err != nil {
		return fmt.Errorf("读章节正文: %w", err)
	}
	m, ok := drama.ModelByID(modelID)
	if !ok {
		return fmt.Errorf("不认识的生成模型 %q", modelID)
	}
	candidate, err := loadProfile(candidatePath, "候选")
	if err != nil {
		return err
	}
	baseline, err := loadProfile(baselinePath, "基线")
	if err != nil {
		return err
	}
	imaged := map[string]bool{}
	for _, a := range assets {
		imaged[a.ID] = imagedFlag == "all"
	}
	for _, id := range strings.Split(imagedFlag, ",") {
		if id = strings.TrimSpace(id); id != "" && id != "all" {
			imaged[id] = true
		}
	}
	input := service.DramaEvalInput{Label: title, Rows: rows, ChapterText: string(text), Title: title, Assets: assets, Imaged: imaged}
	result, err := service.EvalDramaCompile([]service.DramaEvalInput{input}, m, candidate, baseline)
	if err != nil {
		return err
	}
	fmt.Print(result.Markdown)
	if jsonOut != "" {
		raw, _ := json.MarshalIndent(result, "", "  ")
		if err := os.WriteFile(jsonOut, raw, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func readRows(path string) ([]json.RawMessage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读分镜行: %w", err)
	}
	var doc struct {
		Rows []json.RawMessage `json:"rows"`
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		err = json.Unmarshal(raw, &doc.Rows)
	} else {
		err = json.Unmarshal(raw, &doc)
	}
	if err != nil || len(doc.Rows) == 0 {
		return nil, fmt.Errorf("%s 不是分镜行数组或 {\"rows\": [...]}: %v", path, err)
	}
	return doc.Rows, nil
}

// readAssets 读夹具的资产清单（项目资产的 id、标题、分类、文字设定 prompt）；资产主版本写成「id-v1」，与后端用例一致。
func readAssets(path string) ([]service.ProjectAssetSummary, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stub []struct {
		ID       string              `json:"id"`
		Title    string              `json:"title"`
		Category model.AssetCategory `json:"category"`
		Prompt   string              `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &stub); err != nil {
		return nil, fmt.Errorf("%s 格式无效: %w", path, err)
	}
	out := make([]service.ProjectAssetSummary, 0, len(stub))
	for _, a := range stub {
		out = append(out, service.ProjectAssetSummary{ID: a.ID, Title: a.Title, Category: a.Category, PrimaryVersionID: a.ID + "-v1", PreviewText: a.Prompt})
	}
	return out, nil
}

func loadProfile(path string, label string) (*drama.CompileProfile, error) {
	if path == "" {
		return drama.DefaultCompileProfile()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	p, err := drama.ParseCompileProfile(raw, true)
	if err != nil {
		return nil, fmt.Errorf("%s档案 %s: %w", label, path, err)
	}
	return p.WithMeta(-1, label+"："+filepath.Base(path)), nil
}
