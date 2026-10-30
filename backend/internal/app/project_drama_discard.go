// 装段的两道保护（M8 验收修复）：
//   - 会删掉已有产物（分镜图、预演、视频）的装段要用户确认：重新装段 / 先装段、整章重新生成没带 discard 时拒绝（409），
//     details.discards 按段列出会丢的产物；生成任务收尾（改戏类、部分重跑）不在这里拦，它们在建任务前已经确认过影响面。
//   - 带问题装段后用户已接受的问题，在之后的部分重跑 / 改戏类里保持已接受：只认没重写的节里同一条问题，新出现的照常拦成草稿。

package app

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"yingce/backend/internal/drama"

	"gorm.io/gorm"
)

// DramaSegmentDiscard 是装段会删掉的一段和它已有的产物（按类型计数：storyboard 分镜图、action_board 预演、video 视频）。
type DramaSegmentDiscard struct {
	ShotID    string         `json:"shotId"`
	Segment   string         `json:"segment"`
	Section   string         `json:"section,omitempty"`
	Label     string         `json:"label"`
	Artifacts map[string]int `json:"artifacts"`
}

var dramaArtifactLabels = []struct{ kind, label string }{{drama.ArtifactStoryboard, "分镜图"}, {drama.ArtifactActionBoard, "预演"}, {drama.ArtifactVideo, "视频"}}

// dramaSegmentDiscards 是本章这些段（shotIDs 为 nil 时是本章全部段）里有产物的，按段的位置排；失败的产物不算。
func (s *Service) dramaSegmentDiscards(projectID string, unitID string, shotIDs []string) ([]DramaSegmentDiscard, error) {
	shots, err := s.repo.ProjectShots(projectID)
	if err != nil {
		return nil, err
	}
	artifacts, err := s.repo.ProjectShotArtifacts(projectID)
	if err != nil {
		return nil, err
	}
	counts := map[string]map[string]int{}
	for _, a := range artifacts {
		if a.Status == "failed" {
			continue
		}
		if counts[a.ShotID] == nil {
			counts[a.ShotID] = map[string]int{}
		}
		counts[a.ShotID][a.Type]++
	}
	var out []DramaSegmentDiscard
	for _, sh := range shots {
		if sh.UnitID != unitID || len(counts[sh.ID]) == 0 || (shotIDs != nil && !slices.Contains(shotIDs, sh.ID)) {
			continue
		}
		d := DramaSegmentDiscard{ShotID: sh.ID, Label: strings.TrimSpace(sh.Title), Artifacts: counts[sh.ID]}
		if rev, err := s.repo.ShotRevisionForShot(sh.ID, sh.CurrentRevisionID); err == nil {
			var spec struct {
				ID      string `json:"id"`
				Section string `json:"section"`
			}
			_ = json.Unmarshal([]byte(rev.SpecJSON), &spec)
			d.Segment, d.Section = spec.ID, spec.Section
		}
		if d.Segment == "" {
			d.Segment, _, _ = strings.Cut(d.Label, " ")
		}
		out = append(out, d)
	}
	return out, nil
}

// dramaSegmentsDiscardRequired 是「会删掉这些段已有的产物，确认后再装」的拒绝。
func dramaSegmentsDiscardRequired(discards []DramaSegmentDiscard) error {
	parts := make([]string, 0, len(discards))
	for _, d := range discards {
		var items []string
		for _, kind := range dramaArtifactLabels {
			if n := d.Artifacts[kind.kind]; n > 0 {
				items = append(items, fmt.Sprintf("%s %d", kind.label, n))
			}
		}
		name := d.Segment
		if d.Section != "" {
			name += "（" + d.Section + "）"
		}
		parts = append(parts, name+" "+strings.Join(items, "、"))
	}
	err := NewAppError(http.StatusConflict, fmt.Sprintf("这次装段会删掉 %d 段已有的产物：%s。确认后再装段", len(discards), strings.Join(parts, "；")))
	err.Reason = ReasonDramaSegmentsDiscard
	err.Details = map[string]any{"discards": discards}
	return err
}

// guardSegmentDiscards 在要删的段有产物时拒绝（shotIDs 为 nil 时是整章替换，本章全部段都删）。
func (s *Service) guardSegmentDiscards(projectID string, unitID string, shotIDs []string) error {
	if shotIDs != nil && len(shotIDs) == 0 {
		return nil
	}
	discards, err := s.dramaSegmentDiscards(projectID, unitID, shotIDs)
	if err != nil || len(discards) == 0 {
		return err
	}
	return dramaSegmentsDiscardRequired(discards)
}

// ---------------------------------------------------------------- 已接受的问题

// sectionContentKey 认一节的内容：场、正文行、戏类、台词和原样输出都一样才是同一节（没重写的节原样输出不变）。
func sectionContentKey(sec drama.SectionOutput) string {
	sum := sha256.Sum256([]byte(sec.Raw))
	return fmt.Sprintf("%s|%v|%s|%s|%s", sec.Scene, sec.Lines, sec.Genre, strings.Join(sec.LineIDs, ","), hex.EncodeToString(sum[:8]))
}

// shotlistIssueKeys 是一份检查结果里各条 error 的键（规则、台词、所在节的内容；认不出节的整章级问题再加上文字）和出现次数。
func shotlistIssueKeys(report drama.CheckReport, shotlist drama.Shotlist, script *drama.Script, sections []drama.SectionOutput) map[string]int {
	byID := map[string]drama.SectionOutput{}
	for _, sec := range sections {
		byID[sec.ID] = sec
	}
	keys := map[string]int{}
	for _, issue := range drama.ShotIssues(report, shotlist, script, "error") {
		where := "chapter|" + issue.Message
		if sec, ok := byID[issue.Section]; ok && issue.Section != "" {
			where = sectionContentKey(sec)
		}
		keys[issue.Rule+"|"+issue.LineID+"|"+where]++
	}
	return keys
}

// previousAcceptedIssues 是本章当前镜头表里用户已接受（带问题装段）的 error 键；镜头表不是已接受的、不按小节生成的为空。
func (s *Service) previousAcceptedIssues(projectID string, unitID string) (map[string]int, error) {
	record, err := s.repo.DramaShotlistForUnit(projectID, unitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var report drama.CheckReport
	var shotlist drama.Shotlist
	var sections []drama.SectionOutput
	if json.Unmarshal([]byte(firstNonEmpty(record.CheckJSON, "{}")), &report) != nil || !report.Accepted || !report.HasErrors() {
		return nil, nil
	}
	if json.Unmarshal([]byte(record.ShotlistJSON), &shotlist) != nil || strings.TrimSpace(record.SectionsJSON) == "" || json.Unmarshal([]byte(record.SectionsJSON), &sections) != nil {
		return nil, nil
	}
	var script *drama.Script
	if strings.TrimSpace(record.ScriptJSON) != "" {
		var parsed drama.Script
		if json.Unmarshal([]byte(record.ScriptJSON), &parsed) == nil {
			script = &parsed
		}
	}
	return shotlistIssueKeys(report, shotlist, script, sections), nil
}

// issuesAllAccepted 是这次检查的每一条 error 都是之前接受过的（同一节、同一规则、同一句台词，条数不多于之前）。
func issuesAllAccepted(report drama.CheckReport, shotlist drama.Shotlist, script *drama.Script, sections []drama.SectionOutput, accepted map[string]int) bool {
	if len(accepted) == 0 {
		return false
	}
	for key, n := range shotlistIssueKeys(report, shotlist, script, sections) {
		if n > accepted[key] {
			return false
		}
	}
	return true
}

// guardWholeReplace 是整章替换前的确认：guard 为 true 且本章有段带产物时拒绝。
func (s *Service) guardWholeReplace(projectID string, unitID string, guard bool) error {
	if !guard {
		return nil
	}
	return s.guardSegmentDiscards(projectID, unitID, nil)
}
