package app

import (
	"fmt"
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectDualVideoDurationSubmission(t *testing.T) {
	svc, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	shot := model.Shot{ID: "dual-shot", ProjectID: project.ID, UnitID: unit.ID, CurrentRevisionID: "dual-revision"}
	revision := model.ShotRevision{ID: shot.CurrentRevisionID, ShotID: shot.ID, VideoPrompt: "原始镜头"}
	if err := db.Create(&shot).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&revision).Error; err != nil {
		t.Fatal(err)
	}
	for _, content := range []float64{-1, 0, 0.5, 10, 14.999, 15, 15.001, 20, 25, 29.999, 30, 30.001, 45} {
		t.Run(fmt.Sprint(content), func(t *testing.T) {
			durationMs := int64(content * 1000)
			spec := fmt.Sprintf(`{"scene":"A","dur":%g}`, content)
			expected := 30
			if content < 15 {
				expected = 15
			}
			db.Model(&model.Shot{}).Where("id = ?", shot.ID).Update("duration_ms", durationMs)
			db.Model(&model.ShotRevision{}).Where("id = ?", revision.ID).Update("spec_json", spec)
			input := map[string]any{"mode": "video", "capabilityOptions": map[string]any{"videoSeconds": expected}, "metadata": map[string]any{"domainProjectId": project.ID, "shotId": shot.ID, "artifactMetadata": map[string]any{}}, "config": map[string]any{"videoSeconds": fmt.Sprint(expected), "capabilityConfig": ModelCapabilityConfig{Version: 1, Video: &VideoCapabilityConfig{Duration: VideoDurationConfig{Selection: "enum", Values: []int{30, 15, 15}, Default: 15}}}}}
			prompt, err := svc.prepareDramaVideoSubmission("user-1", input, "原始镜头", false)
			if content <= 0 || content > 30 {
				if err == nil {
					t.Fatal("invalid duration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			config := input["config"].(map[string]any)
			metadata := input["metadata"].(map[string]any)["artifactMetadata"].(map[string]any)
			if config["videoSeconds"] != fmt.Sprint(expected) || input["capabilityOptions"].(map[string]any)["videoSeconds"] != expected || metadata["contentDurationSeconds"] != content || metadata["durationSeconds"] != expected {
				t.Fatalf("quote/submission differs: %+v", input)
			}
			if err := svc.enforceDramaSegmentTaskDuration("user-1", input); err != nil {
				t.Fatal(err)
			}
			intent := ModelRequestIntentFromTaskInput(input, "canvas_video", "reference_to_video")
			if fmt.Sprint(intent.Options["videoSeconds"]) != fmt.Sprint(expected) {
				t.Fatalf("billing intent differs from submitted duration: %+v", intent)
			}
			for range 2 {
				prompt, err = svc.prepareDramaVideoSubmission("user-1", input, prompt, true)
				if err != nil {
					t.Fatal(err)
				}
			}
			if prompt != "原始镜头" {
				t.Fatal("retry changed original prompt")
			}
			var stored model.Shot
			db.First(&stored, "id = ?", shot.ID)
			var storedRevision model.ShotRevision
			db.First(&storedRevision, "id = ?", revision.ID)
			if stored.DurationMs != durationMs || storedRevision.SpecJSON != spec {
				t.Fatal("submission changed original content duration")
			}
			if content >= 15 {
				config["videoSeconds"] = "15"
				if _, err := svc.prepareDramaVideoSubmission("user-1", input, prompt, false); err == nil {
					t.Fatal("stale 15-second quote silently upgraded")
				}
				if svc.enforceDramaSegmentTaskDuration("user-1", input) == nil {
					t.Fatal("underreported submission duration accepted")
				}
			}
		})
	}
	for _, values := range [][]int{{4, 30}, {5, 15, 30}, {0, 15, 30}} {
		if len(projectVideoDurationSlots(VideoDurationConfig{Selection: "enum", Values: values})) != 0 {
			t.Fatalf("unrelated model was padded: %v", values)
		}
	}
}
