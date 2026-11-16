package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"yingce/backend/internal/drama"
)

// guardDirectorSegmentWrites 守住导演分镜段（SSS、本体论装出的段，spec.verbatim 非空）：视频提交用的是 verbatim
// （单元卡的粘贴提示词原文，@图k 与 refs 顺序一一对应），shots 只供分镜卡显示。普通写段（PUT /drama/segments、
// Agent drama_segments_write）若只改了 shots 或 refs，画面上看到的改了、实际提交的没变，所以：
//   - 已有的导演段只能原样写回（内容不变），不能改画面、台词、时长、参考，不能拆、合、被删、被继承；
//   - 普通写段不能新写出 verbatim（原文只由导演分镜收尾和返修写，它们走 writeDramaSegments，不经过这里）。
//
// 要改导演段只能在「导演文档」对这一段返修（重写单元卡并重新编译，只替换这一段）。原流程的段（verbatim 为空）照旧可编辑。
func (s *Service) guardDirectorSegmentWrites(userID string, projectID string, req DramaSegmentsWriteRequest) error {
	unitID := strings.TrimSpace(req.UnitID)
	if unitID == "" {
		return nil
	}
	existing, err := s.DramaSegments(userID, projectID, unitID)
	if err != nil {
		return err
	}
	director := map[string]drama.Segment{}
	for _, view := range existing {
		if !view.Derived && strings.TrimSpace(view.Spec.Verbatim) != "" {
			director[view.Spec.ID] = view.Spec
		}
	}
	for _, raw := range req.Segments {
		seg, err := drama.ParseSegment(raw)
		if err != nil {
			return BadAuthRequest(err.Error())
		}
		stored, isDirector := director[seg.ID]
		if !isDirector {
			if strings.TrimSpace(seg.Verbatim) != "" {
				return directorSegmentReadonlyError(seg.ID, "粘贴提示词原文只能由导演分镜生成或返修写入")
			}
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return BadAuthRequest(err.Error())
		}
		if _, present := fields["refs"]; !present {
			// 没带 refs 的写入按原有参考补（与 writeDramaSegmentsTransaction 同一规则），只比较其余内容。
			seg.Refs = stored.Refs
		}
		if !reflect.DeepEqual(seg, stored) {
			return directorSegmentReadonlyError(seg.ID, "视频提交用的是单元卡的粘贴提示词原文，改画面、台词、时长或参考不会进提交")
		}
	}
	if len(req.Order) > 0 {
		kept := map[string]bool{}
		for _, id := range req.Order {
			kept[strings.TrimSpace(id)] = true
		}
		for id := range director {
			if !kept[id] {
				return directorSegmentReadonlyError(id, "导演分镜单元不能拆、合或删除")
			}
		}
	}
	for target, sources := range req.Inherit {
		for _, id := range append([]string{target}, sources...) {
			if _, ok := director[id]; ok {
				return directorSegmentReadonlyError(id, "导演分镜单元不能拆、合")
			}
		}
	}
	return nil
}

func directorSegmentReadonlyError(segmentID string, why string) error {
	err := NewAppError(http.StatusConflict, fmt.Sprintf("%s 是导演分镜单元，%s；要改请在「导演文档」里对这一段返修（只替换这一段）", drama.EchoSegmentID(segmentID), why))
	err.Reason = ReasonDirectorSegmentReadonly
	err.Details = map[string]any{"segment": segmentID}
	return err
}
