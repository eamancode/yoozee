package app

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// DramaDeliveryPackage 是交付包：一个 ZIP 和它的文件名。
type DramaDeliveryPackage struct {
	FileName string
	Data     []byte
	Evidence model.ShotEvidence
}

type deliveryFilm struct {
	TaskID      string `json:"taskId"`
	ResourceID  string `json:"resourceId"`
	FileName    string `json:"fileName"`
	DurationMs  int64  `json:"durationMs"`
	CompletedAt string `json:"completedAt"`
	SubtitleSRT string `json:"-"`
}

// latestProjectFilm 找项目里最近一次成功的时间线渲染（剪辑成片的导出）。
func (s *Service) latestProjectFilm(userID string, projectID string) *deliveryFilm {
	summaries, err := s.TasksWithOptions(userID, TaskListOptions{Limit: 100, ProjectID: projectID})
	if err != nil {
		return nil
	}
	var best *deliveryFilm
	var bestAt time.Time
	for _, summary := range summaries {
		if summary.Type != model.TaskTypeTimelineRender || summary.Status != model.TaskStatusSucceeded {
			continue
		}
		task, err := s.repo.TaskForUser(userID, summary.ID)
		if err != nil || task.CompletedAt == nil {
			continue
		}
		var result timelineRenderResult
		if json.Unmarshal([]byte(task.ResultJSON), &result) != nil || result.ResourceID == "" {
			continue
		}
		if best == nil || task.CompletedAt.After(bestAt) {
			bestAt = *task.CompletedAt
			best = &deliveryFilm{TaskID: task.ID, ResourceID: result.ResourceID, FileName: result.FileName, DurationMs: result.DurationMs, CompletedAt: task.CompletedAt.Format(time.RFC3339), SubtitleSRT: result.SubtitleSRT}
		}
	}
	return best
}

// DramaDelivery 打交付包：需求文档、连戏表、分段表（JSON + CSV）、资产清单、每段生成参数、证据、进度与验收单、字幕。
// 成片本体不放进包里（太大），以资源 ID 和下载路径列出。包里不放任何密钥。
func (s *Service) DramaDelivery(userID string, projectID string) (DramaDeliveryPackage, error) {
	project, err := s.activeProjectForUser(userID, projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	brief, err := s.ProjectBrief(userID, projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	bible, err := s.ProjectBible(userID, projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	segments, err := s.DramaSegments(userID, projectID, "")
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	progress, err := s.DramaProgress(userID, projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	artifacts, err := s.repo.ProjectShotArtifacts(projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	evidence, err := s.repo.ProjectShotEvidence(projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	assets, err := s.ProjectAssets(userID, projectID)
	if err != nil {
		return DramaDeliveryPackage{}, err
	}
	film := s.latestProjectFilm(userID, projectID)

	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	write := func(name string, data []byte) error {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.Modified = time.Now()
		w, err := archive.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	writeJSON := func(name string, value any) error {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return err
		}
		return write(name, data)
	}

	selected := map[string]model.ShotArtifact{}
	takes := map[string]int{}
	for _, a := range artifacts {
		if a.Type != drama.ArtifactVideo || a.Status == "failed" || a.Status == "stale" {
			continue
		}
		takes[a.ShotID]++
		if a.Selected {
			selected[a.ShotID] = a
		}
	}
	type segmentRow struct {
		ShotID             string          `json:"shotId"`
		Segment            drama.Segment   `json:"segment"`
		Derived            bool            `json:"derived"`
		DurationMs         int64           `json:"durationMs"`
		Takes              int             `json:"takes"`
		SelectedArtifactID string          `json:"selectedArtifactId,omitempty"`
		SelectedResourceID string          `json:"selectedResourceId,omitempty"`
		GenerationParams   json.RawMessage `json:"generationParams,omitempty"`
	}
	rows := make([]segmentRow, 0, len(segments))
	params := map[string]json.RawMessage{}
	for _, view := range segments {
		row := segmentRow{ShotID: view.ShotID, Segment: view.Spec, Derived: view.Derived, DurationMs: view.DurationMs, Takes: takes[view.ShotID]}
		if a, ok := selected[view.ShotID]; ok {
			row.SelectedArtifactID, row.SelectedResourceID = a.ID, a.ResourceID
			if json.Valid([]byte(a.MetadataJSON)) {
				row.GenerationParams = json.RawMessage(a.MetadataJSON)
				params[view.Spec.ID] = row.GenerationParams
			}
		}
		rows = append(rows, row)
	}

	var csvBuffer bytes.Buffer
	csvBuffer.WriteString("\ufeff")
	table := csv.NewWriter(&csvBuffer)
	_ = table.Write([]string{"段", "场", "时长（秒）", "模式", "镜头数", "台词", "候选", "选中", "剧本", "参考", "生成", "过审", "入片", "验收"})
	progressByShot := map[string]drama.SegmentProgress{}
	for _, p := range progress.Segments {
		progressByShot[p.ShotID] = p
	}
	mark := func(ok bool) string {
		if ok {
			return "✓"
		}
		return ""
	}
	for _, row := range rows {
		p := progressByShot[row.ShotID]
		_ = table.Write([]string{row.Segment.ID, row.Segment.Scene, fmt.Sprintf("%.1f", float64(row.DurationMs)/1000), row.Segment.Mode, fmt.Sprint(len(row.Segment.Shots)), row.Segment.Dialogue(), fmt.Sprint(row.Takes), row.SelectedArtifactID, mark(p.Script), mark(p.Refs), mark(p.Generated), mark(p.Reviewed), mark(p.InCut), mark(p.Accepted)})
	}
	table.Flush()

	var assetBuffer bytes.Buffer
	assetBuffer.WriteString("\ufeff")
	assetTable := csv.NewWriter(&assetBuffer)
	_ = assetTable.Write([]string{"资产 ID", "名称", "类别", "媒体", "主版本", "版本数", "用在哪里"})
	sort.SliceStable(assets, func(i, j int) bool { return assets[i].Title < assets[j].Title })
	for _, asset := range assets {
		_ = assetTable.Write([]string{asset.ID, asset.Title, string(asset.Category), asset.MediaType, asset.PrimaryVersionID, fmt.Sprint(asset.VersionCount), strings.Join(asset.Usages, "、")})
	}
	assetTable.Flush()

	m, _ := projectDramaModel(project)
	var readme strings.Builder
	fmt.Fprintf(&readme, "# %s —— 交付包\n\n", project.Name)
	fmt.Fprintf(&readme, "- 生成时间：%s\n- 生成模型：%s（一段最长 %d 秒）\n- 进度：%s\n", time.Now().Format("2006-01-02 15:04"), m.Label, m.Duration.Max, progress.Line)
	if film != nil {
		fmt.Fprintf(&readme, "- 成片：%s（资源 ID %s，时长 %.1f 秒，导出于 %s）。在柚子里打开：/api/resources/%s/file\n", firstNonEmpty(film.FileName, "成片"), film.ResourceID, float64(film.DurationMs)/1000, film.CompletedAt, film.ResourceID)
	} else {
		readme.WriteString("- 成片：还没有导出（在「剪辑成片」页导出后再打包）\n")
	}
	readme.WriteString("\n## 八件事\n\n| 终态 | 成立 | 说明 |\n| --- | --- | --- |\n")
	for _, state := range progress.EndStates {
		fmt.Fprintf(&readme, "| %s | %s | %s |\n", state.Label, map[bool]string{true: "✓", false: "—"}[state.Done], state.Detail)
	}
	readme.WriteString("\n## 文件\n\n- `需求文档.json`、`连戏表.json`：契约 brief、bible\n- `分段表.json`、`分段表.csv`：每段的中性分段、候选数和选中的候选\n- `生成参数.json`：每段选中候选的生成参数\n- `资产清单.csv`：项目素材库\n- `证据.json`：每条候选的听写、抽帧、人脸、门禁记录\n- `验收单.md`：请甲方逐项填写\n")
	if film != nil && film.SubtitleSRT != "" {
		readme.WriteString("- `字幕.srt`：成片字幕\n")
	}

	var acceptance strings.Builder
	fmt.Fprintf(&acceptance, "# %s —— 验收单（%s）\n\n", project.Name, time.Now().Format("2006-01-02"))
	if film != nil {
		fmt.Fprintf(&acceptance, "交付物：%s（%.1f 秒）\n\n", firstNonEmpty(film.FileName, "成片"), float64(film.DurationMs)/1000)
	}
	acceptance.WriteString("## 需求文档里的验收标准\n\n| # | 验收项 | 在哪看（时间码） | 结论（通过 / 要改） | 要改什么（时间码 + 一句话） |\n| --- | --- | --- | --- | --- |\n")
	if value, ok := brief["acceptance"].(map[string]any); ok {
		if text, ok := value["value"].(string); ok && strings.TrimSpace(text) != "" {
			for i, item := range strings.FieldsFunc(text, func(r rune) bool { return r == '；' || r == ';' || r == '\n' }) {
				fmt.Fprintf(&acceptance, "| %d | %s |  |  |  |\n", i+1, strings.TrimSpace(item))
			}
		}
	}
	acceptance.WriteString("\n## 其他意见\n\n- \n\n验收人：　　　　日期：\n")

	files := []struct {
		name  string
		write func() error
	}{
		{"README.md", func() error { return write("README.md", []byte(readme.String())) }},
		{"需求文档.json", func() error { return writeJSON("需求文档.json", brief) }},
		{"连戏表.json", func() error { return writeJSON("连戏表.json", bible) }},
		{"分段表.json", func() error { return writeJSON("分段表.json", rows) }},
		{"分段表.csv", func() error { return write("分段表.csv", csvBuffer.Bytes()) }},
		{"生成参数.json", func() error { return writeJSON("生成参数.json", params) }},
		{"资产清单.csv", func() error { return write("资产清单.csv", assetBuffer.Bytes()) }},
		{"证据.json", func() error { return writeJSON("证据.json", evidence) }},
		{"进度.json", func() error { return writeJSON("进度.json", progress) }},
		{"验收单.md", func() error { return write("验收单.md", []byte(acceptance.String())) }},
	}
	if film != nil && film.SubtitleSRT != "" {
		files = append(files, struct {
			name  string
			write func() error
		}{"字幕.srt", func() error { return write("字幕.srt", []byte(film.SubtitleSRT)) }})
	}
	for _, f := range files {
		if err := f.write(); err != nil {
			return DramaDeliveryPackage{}, fmt.Errorf("写入交付包 %s 失败: %w", f.name, err)
		}
	}
	if err := archive.Close(); err != nil {
		return DramaDeliveryPackage{}, err
	}
	metrics, _ := json.Marshal(map[string]any{"files": len(files), "bytes": buffer.Len(), "filmResourceId": func() string {
		if film != nil {
			return film.ResourceID
		}
		return ""
	}()})
	record := model.ShotEvidence{ID: newID(), ProjectID: projectID, Kind: drama.EvidenceDelivery, MetricsJSON: string(metrics), Note: "交付包已生成", CreatedBy: userID, CreatedAt: time.Now()}
	if film != nil {
		record.ResourceID = film.ResourceID
	}
	if err := s.repo.CreateShotEvidence(&record); err != nil {
		return DramaDeliveryPackage{}, err
	}
	name := fmt.Sprintf("%s_交付包_%s.zip", sanitizeDeliveryName(project.Name), time.Now().Format("20060102-1504"))
	return DramaDeliveryPackage{FileName: name, Data: buffer.Bytes(), Evidence: record}, nil
}

func sanitizeDeliveryName(name string) string {
	name = strings.TrimSpace(name)
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	name = replacer.Replace(name)
	if name == "" {
		return "短剧"
	}
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	return name
}
