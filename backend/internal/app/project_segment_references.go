package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

func segmentReferenceWriteError(err error) error {
	if errors.Is(err, repository.ErrProjectUnitShotsChanged) {
		conflict := NewAppError(http.StatusConflict, "分段在保存期间已更新，请刷新后重试")
		conflict.Reason = ReasonConflict
		return conflict
	}
	return err
}

func (s *Service) segmentReferencePlan(userID, projectID, shotID, expected string, spec string, now time.Time) ([]repository.SegmentReferenceSync, error) {
	var seg drama.Segment
	if json.Unmarshal([]byte(spec), &seg) != nil || seg.ID == "" {
		return nil, nil
	}
	plan := repository.SegmentReferenceSync{ExpectedRevisionID: expected}
	seen := map[string]bool{}
	for _, ref := range seg.Refs {
		version, err := s.repo.ProjectReferenceVersion(userID, projectID, ref.File)
		if err != nil {
			return nil, BadAuthRequest(fmt.Sprintf("分段参考 %s 不属于本项目或版本不可用", ref.File))
		}
		if !seen[version.ID] {
			plan.References = append(plan.References, model.ShotAssetReference{ID: newID(), ShotID: shotID, AssetVersionID: version.ID, CreatedAt: now})
			seen[version.ID] = true
		}
	}
	return []repository.SegmentReferenceSync{plan}, nil
}

// Called inside the link/unlink transaction; the spec and its association rows
// commit together. The current visual versions stay pinned, including child cards.
func (s *Service) changeSegmentAsset(userID, projectID, shotID, addVersion, replaceID, removeID string) (bool, error) {
	shot, err := s.repo.LockShotForProject(projectID, shotID)
	if err != nil {
		return false, err
	}
	if shot.CurrentRevisionID == "" {
		return false, nil
	}
	revision, err := s.repo.ShotRevisionForShot(shotID, shot.CurrentRevisionID)
	if err != nil {
		return false, err
	}
	var seg drama.Segment
	if json.Unmarshal([]byte(revision.SpecJSON), &seg) != nil || seg.ID == "" {
		return false, nil
	}
	if seg.Mode == "lock_first_frame" && addVersion != "" {
		return true, BadAuthRequest("首尾帧模式不能追加普通参考图，请先切换生成模式")
	}
	refs, err := s.repo.ShotAssetReferencesForShot(shotID)
	if err != nil {
		return true, err
	}
	removeVersion := ""
	if id := firstNonEmpty(removeID, replaceID); id != "" {
		i := slices.IndexFunc(refs, func(r model.ShotAssetReference) bool { return r.ID == id })
		if i < 0 {
			return true, NotFound("镜头资产引用不存在")
		}
		if refs[i].Role != "reference" {
			return false, nil
		}
		removeVersion = refs[i].AssetVersionID
	}
	oldRefs := slices.Clone(seg.Refs)
	index := -1
	for i, ref := range seg.Refs {
		v, err := s.repo.ProjectReferenceVersion(userID, projectID, ref.File)
		if err != nil {
			return true, BadAuthRequest("现有分段参考不可用，请先处理该参考")
		}
		if v.ID == removeVersion {
			index = i
			break
		}
	}
	if removeID != "" && index < 0 {
		return false, nil
	}
	if addVersion != "" {
		version, err := s.repo.ProjectReferenceVersion(userID, projectID, addVersion)
		if err != nil {
			return true, err
		}
		asset, err := s.repo.AssetForUser(userID, version.AssetID)
		if err != nil {
			return true, err
		}
		if replaceID != "" {
			previous, err := s.repo.ProjectReferenceVersion(userID, projectID, removeVersion)
			if err != nil {
				return true, err
			}
			previousAsset, err := s.repo.AssetForUser(userID, previous.AssetID)
			if err != nil {
				return true, err
			}
			if previousAsset.Category != asset.Category {
				return true, BadAuthRequest("请使用同类型资产更换当前引用")
			}
		}
		if index >= 0 && slices.ContainsFunc(seg.Refs, func(r drama.Ref) bool { return r.File == version.ID && r.File != seg.Refs[index].File }) {
			return true, BadAuthRequest("该资产已在本段参考中，请选择其他资产")
		}
		role := "prop"
		if asset.Category == model.AssetCategoryCharacter {
			role = "identity"
		}
		if asset.Category == model.AssetCategoryEnvironment {
			role = "location"
		}
		if index >= 0 {
			seg.Refs[index].File = version.ID
		} else {
			if !slices.ContainsFunc(seg.Refs, func(r drama.Ref) bool { return r.File == version.ID }) {
				seg.Refs = append(seg.Refs, drama.Ref{File: version.ID, Role: role, Note: asset.Title})
			}
		}
	} else if index >= 0 {
		seg.Refs = append(seg.Refs[:index], seg.Refs[index+1:]...)
		seg.Verbatim = remapRemovedReference(seg.Verbatim, oldRefs, index)
	}
	if removeVersion != "" {
		for i := range seg.Shots {
			var kept []string
			for _, file := range seg.Shots[i].Refs {
				if file == removeVersion || (index >= 0 && file == oldRefs[index].File) {
					if addVersion != "" {
						kept = append(kept, addVersion)
					}
				} else {
					kept = append(kept, file)
				}
			}
			seg.Shots[i].Refs = kept
		}
	}
	if seg.Mode == "text_only" && len(seg.Refs) > 0 {
		seg.Mode = "lock_identity"
	}
	if seg.Mode == "lock_identity" && len(seg.Refs) == 0 {
		seg.Mode = "text_only"
	}
	raw, err := json.Marshal(seg)
	if err != nil {
		return true, err
	}
	input := ShotRevisionInput{Spec: raw, PlotDescription: revision.PlotDescription, Action: revision.Action, Dialogue: revision.Dialogue,
		ShotSize: revision.ShotSize, CameraAngle: revision.CameraAngle, CameraMovement: revision.CameraMovement,
		DurationMs: revision.DurationMs, ImagePrompt: revision.ImagePrompt, VideoPrompt: revision.VideoPrompt,
		NegativePrompt: revision.NegativePrompt, ContinuityNotes: revision.ContinuityNotes}
	if err := json.Unmarshal([]byte(revision.ActionBeatsJSON), &input.ActionBeats); err != nil {
		return true, err
	}
	_, err = s.CreateProjectShot(userID, projectID, CreateProjectShotRequest{ID: shotID, UnitID: shot.UnitID, Title: shot.Title,
		Description: shot.Description, Position: shot.Position, DurationMs: shot.DurationMs, Status: shot.Status, Revision: input})
	if err != nil {
		return true, err
	}
	if removeVersion != "" {
		for _, ref := range refs {
			if ref.AssetVersionID == removeVersion && ref.Source != "segment" && ref.Role == "reference" {
				_, err = s.repo.DeleteShotAssetReferenceAndInvalidate(projectID, shotID, ref.ID, time.Now())
				if err != nil {
					return true, err
				}
			}
		}
	}
	return true, nil
}

var segmentImageLabel = regexp.MustCompile(`@(?:图片|图)(\d+)`)

func remapRemovedReference(prompt string, refs []drama.Ref, removed int) string {
	return segmentImageLabel.ReplaceAllStringFunc(prompt, func(tag string) string {
		match := segmentImageLabel.FindStringSubmatch(tag)
		n, _ := strconv.Atoi(match[1])
		if n == removed+1 {
			return firstNonEmpty(refs[removed].Note, "原参考素材")
		}
		if n > removed+1 {
			return strings.TrimSuffix(tag, match[1]) + strconv.Itoa(n-1)
		}
		return tag
	})
}
