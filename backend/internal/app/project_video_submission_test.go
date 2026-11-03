package app

import (
	"fmt"
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectFixedVideoDurations(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "minimax-h3")
	shot := model.Shot{ID: "s1", ProjectID: project.ID, UnitID: unit.ID, CurrentRevisionID: "r1", DurationMs: 20000}
	revision := model.ShotRevision{ID: "r1", ShotID: "s1", SpecJSON: `{"scene":"A","dur":20}`}
	if err := db.Create(&shot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	for _, slot := range []int{15, 30} {
		for _, content := range []float64{0, -1, 0.5, 10, 15, 15.001, 20, 30, 30.001} {
			for _, selection := range []string{"enum", "range"} {
				t.Run(fmt.Sprintf("%d/%g/%s", slot, content, selection), func(t *testing.T) {
					spec := fmt.Sprintf(`{"scene":"A","dur":%g}`, content)
					db.Model(&model.Shot{}).Where("id = ?", shot.ID).Update("duration_ms", int64(content*1000))
					db.Model(&model.ShotRevision{}).Where("id = ?", revision.ID).Update("spec_json", spec)
					duration := VideoDurationConfig{Selection: selection, Values: []int{slot}, Min: slot, Max: slot}
					input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": shot.ID}, "config": map[string]any{"videoSeconds": "20", "capabilityConfig": ModelCapabilityConfig{Version: 1, Video: &VideoCapabilityConfig{Duration: duration}}}}
					prompt, err := svc.prepareDramaVideoSubmission("user-1", input, "原始镜头", false)
					if content <= 0 || content > float64(slot) {
						if err == nil {
							t.Fatal("invalid duration accepted")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if prompt != "原始镜头" || input["config"].(map[string]any)["videoSeconds"] != fmt.Sprint(slot) {
						t.Fatalf("unexpected submission: %+v", input)
					}
					if err := svc.enforceDramaSegmentTaskDuration("user-1", input); err != nil {
						t.Fatal(err)
					}
				})
			}
		}
	}
	if fixedProjectVideoSeconds(VideoDurationConfig{Selection: "enum", Values: []int{4, 30}}) != 0 {
		t.Fatal("multi-slot model must retain ordinary rules")
	}
}

func TestProjectSceneEndingUsesLatestOrderAndNeverStacks(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "seedance-2.0")
	for i, scene := range []string{"A", "A", "A", "B"} {
		shot := model.Shot{ID: fmt.Sprint(i), ProjectID: project.ID, UnitID: unit.ID, CurrentRevisionID: fmt.Sprint("r", i), Position: i, DurationMs: 10000}
		revision := model.ShotRevision{ID: shot.CurrentRevisionID, ShotID: shot.ID, VideoPrompt: "原文", SpecJSON: fmt.Sprintf(`{"scene":%q,"dur":10}`, scene)}
		if err := db.Create(&shot).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&revision).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := range 4 {
		input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": fmt.Sprint(i)}, "config": map[string]any{"videoSeconds": "10"}}
		prompt, err := svc.prepareDramaVideoSubmission("user-1", input, "完整镜头内容", false)
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if i < 2 {
			want = 1
		}
		for range 3 {
			prompt, err = svc.prepareDramaVideoSubmission("user-1", input, prompt, true)
			if err != nil {
				t.Fatal(err)
			}
		}
		if strings.Count(prompt, dramaSceneEnding) != want || !strings.HasPrefix(prompt, "完整镜头内容") {
			t.Fatalf("group %d: %q", i, prompt)
		}
		if i == 0 {
			db.Model(&model.Shot{}).Where("id = ?", "3").Update("position", 0)
			db.Model(&model.Shot{}).Where("id = ?", "0").Update("position", -1)
			prompt, err = svc.prepareDramaVideoSubmission("user-1", input, prompt, true)
			if err != nil || prompt != "完整镜头内容" {
				t.Fatalf("reordered different scene must remove generated suffix: %q %v", prompt, err)
			}
			db.Model(&model.Shot{}).Where("id = ?", "3").Update("position", 3)
			db.Model(&model.Shot{}).Where("id = ?", "0").Update("position", 0)
		}
	}
	var stored model.ShotRevision
	db.First(&stored, "id = ?", "r0")
	if stored.VideoPrompt != "原文" || strings.Contains(stored.SpecJSON, dramaSceneEnding) {
		t.Fatal("submission changed editable content")
	}
	input := map[string]any{"mode": "video", "metadata": map[string]any{"domainProjectId": project.ID, "shotId": "0", "shotRevisionId": "stale"}}
	if _, err := svc.prepareDramaVideoSubmission("user-1", input, "原文", false); err == nil {
		t.Fatal("stale revision accepted")
	}
	if _, err := svc.prepareDramaVideoSubmission("other-user", input, "原文", false); err == nil {
		t.Fatal("foreign project accepted")
	}
}

func TestProjectSourceCharacterLimits(t *testing.T) {
	for _, content := range []string{strings.Repeat("雨", 150000), "<p>" + strings.Repeat("雨", 150000) + "</p>", strings.Repeat("😀", 150000)} {
		if err := validateProjectSourceSize(content); err != nil {
			t.Fatal(err)
		}
		if err := validateProjectSourceSize(content + "字"); err == nil {
			t.Fatal("150001 characters accepted")
		}
	}
}
