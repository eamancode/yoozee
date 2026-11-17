package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

// Split and merged cards can retain the old segment-level shot_ids. Prefer
// the actual displayed sub-shots; use the parallel IDs only for older rows.
func segmentSourceShotIDs(seg drama.Segment) []string {
	var ids []string
	for i, sub := range seg.Shots {
		id := sub.ShotID
		if id == "" && len(seg.ShotIDs) == len(seg.Shots) {
			id = seg.ShotIDs[i]
		}
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func removeShotlistUnit(r *repository.Repository, project *model.Project, shot *model.Shot, seg drama.Segment, remaining []drama.Segment) error {
	if seg.Verbatim != "" {
		return nil // Director cards are packed from the versioned episode document.
	}
	removed := map[string]bool{}
	for _, id := range segmentSourceShotIDs(seg) {
		removed[id] = true
	}
	if len(removed) == 0 {
		return nil
	}
	record, err := r.DramaShotlistForUnit(project.ID, shot.UnitID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, other := range remaining {
		for _, id := range segmentSourceShotIDs(other) {
			delete(removed, id)
		}
	}
	var list drama.Shotlist
	if err := json.Unmarshal([]byte(record.ShotlistJSON), &list); err != nil {
		return NewAppError(http.StatusConflict, "镜头表无法读取，单元未删除")
	}
	if !slices.ContainsFunc(list.Shots, func(sh drama.Shot) bool { return removed[sh.ID] }) {
		return nil
	}
	var rows []json.RawMessage
	hasRows := strings.TrimSpace(record.RowsJSON) != ""
	if hasRows && (json.Unmarshal([]byte(record.RowsJSON), &rows) != nil || len(rows) != len(list.Shots)) {
		return NewAppError(http.StatusConflict, "镜头表和保存的分镜行不一致，无法确认删除范围")
	}
	keptRows := make([]json.RawMessage, 0, len(rows))
	keptShots := make([]drama.Shot, 0, len(list.Shots))
	for i, sh := range list.Shots {
		if removed[sh.ID] {
			continue
		}
		keptShots = append(keptShots, sh)
		if hasRows {
			row := rows[i]
			var source struct {
				Spec struct {
					ID string `json:"id"`
				} `json:"spec"`
			}
			// Old rows can derive an ID from their ordinal; pin the resolved ID
			// so compacting the rows cannot recycle a deleted sub-shot ID.
			if json.Unmarshal(row, &source) != nil || source.Spec.ID != sh.ID {
				if row, err = withRowSpec(row, sh); err != nil {
					return err
				}
			}
			keptRows = append(keptRows, row)
		}
	}
	list.Shots = keptShots
	if hasRows {
		raw, err := json.Marshal(keptRows)
		if err != nil {
			return err
		}
		record.RowsJSON = string(raw)
	}
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	record.ShotlistJSON = string(raw)
	for _, field := range []*string{&record.SectionsJSON, &record.PackedSectionsJSON} {
		if strings.TrimSpace(*field) == "" {
			continue
		}
		var sections []drama.SectionOutput
		if err := json.Unmarshal([]byte(*field), &sections); err != nil {
			return NewAppError(http.StatusConflict, "镜头表的小节记录无法读取，单元未删除")
		}
		kept := sections[:0]
		for _, section := range sections {
			n := len(section.Shots)
			section.Shots = slices.DeleteFunc(section.Shots, func(id string) bool { return removed[id] })
			if n > 0 && len(section.Shots) == 0 {
				continue
			}
			kept = append(kept, section)
		}
		*field = sectionsJSON(kept)
	}
	oldCheck := drama.CheckReport{}
	if err := json.Unmarshal([]byte(firstNonEmpty(record.CheckJSON, "{}")), &oldCheck); err != nil {
		return err
	}
	var script *drama.Script
	if strings.TrimSpace(record.ScriptJSON) != "" {
		if err := json.Unmarshal([]byte(record.ScriptJSON), &script); err != nil {
			return err
		}
	}
	svc := &Service{repo: r}
	assets, err := svc.ProjectAssets(project.UserID, project.ID)
	if err != nil {
		return err
	}
	pc := projectDramaContext(project)
	unit, err := r.ProjectUnit(project.ID, shot.UnitID)
	if err != nil {
		return err
	}
	pc.Episode = svc.dramaChapterEpisode(project.ID, unit)
	ctx, _, err := dramaCheckContext(script, pc, assets)
	if err != nil {
		return err
	}
	ctx.WarnOnly = drama.GenreRelaxedRules(drama.ShotlistGenre(list.Shots))
	check := drama.CheckShots(list, ctx)
	rules, err := drama.BuiltinSegmentRules()
	if err != nil {
		return err
	}
	if m, modelErr := dramaShotlistModel(project, briefSegmentRule(project.BriefJSON, nil)); modelErr == nil {
		check.CheckSectionDurations(list, script, m, rules)
	}
	check.CheckSegmentTotal(remaining, pc.targetSeconds(), rules)
	check.Parse = oldCheck.Parse
	check.Accepted = check.HasErrors() && (!oldCheck.HasErrors() || oldCheck.Accepted)
	raw, err = json.Marshal(check)
	if err != nil {
		return err
	}
	record.CreatedBy, record.UpdatedAt, record.CheckJSON = project.UserID, time.Now(), string(raw)
	err = r.SavePrunedShotlist(record)
	if errors.Is(err, repository.ErrProjectUnitShotsChanged) {
		return NewAppError(http.StatusConflict, "本集镜头表已更新，请刷新后重新确认")
	}
	return err
}

func reconnectAfterShotDelete(r *repository.Repository, shot *model.Shot, deleted drama.Segment, others []model.ShotRevision, userID string) ([]drama.Segment, error) {
	remaining := make([]drama.Segment, 0, len(others))
	var prev drama.Segment
	for _, item := range others {
		var seg drama.Segment
		if item.SpecJSON != "" {
			if err := json.Unmarshal([]byte(item.SpecJSON), &seg); err != nil {
				return nil, NewAppError(http.StatusConflict, "相邻单元的镜头来源无法读取，请核对本集镜头表")
			}
		}
		if b := seg.ContinuityBridge; b != nil && seg.Scene == deleted.Scene && b.PrevSegment == deleted.ID {
			seg.ContinuityBridge = nil
			if prev.ID != "" && prev.Scene == seg.Scene {
				profile, err := (&Service{repo: r}).activeCompileProfile()
				if err != nil {
					return nil, err
				}
				var shared []string
				for _, ref := range prev.Refs {
					if ref.Role != "motion" && slices.ContainsFunc(seg.Refs, func(other drama.Ref) bool { return other.File == ref.File }) {
						shared = append(shared, ref.File)
					}
				}
				seg.ContinuityBridge = drama.NewContinuityBridge(&prev, shared, profile)
			}
			next, err := r.LockShotForProject(shot.ProjectID, item.ShotID)
			if err != nil {
				return nil, err
			}
			if next.CurrentRevisionID != item.ID {
				return nil, NewAppError(http.StatusConflict, "相邻分镜已更新，请刷新后重新确认")
			}
			if err := guardStoryboardTasks(r, userID, next, "需要更新同场衔接的单元 "+seg.ID); err != nil {
				return nil, err
			}
			// Preserve the script, prompts, bindings and earlier takes. A fresh
			// revision pins future compilation and quotes to the new handoff.
			rev, err := r.ShotRevisionForShot(next.ID, item.ID)
			if err != nil {
				return nil, err
			}
			rev.ID, rev.SpecJSON, rev.CreatedBy, rev.CreatedAt = newID(), seg.Canonical(), userID, time.Now()
			next.UpdatedAt = rev.CreatedAt
			if err := r.SaveShotWithRevisionSelective(next, rev, false, staleDeciderFor(rev)); err != nil {
				return nil, err
			}
		}
		seg.Dur = float64(item.DurationMs) / 1000
		remaining = append(remaining, seg)
		prev = seg
	}
	return remaining, nil
}
