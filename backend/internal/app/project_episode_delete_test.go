package app

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

func TestEpisodeDeleteReportsActiveDirectorWhileMutexIsHeld(t *testing.T) {
	s, db, project, unit := newProjectDramaTestService(t, "seedance-2.5")
	if err := db.Model(&unit).Update("kind", model.ProjectUnitKindEpisode).Error; err != nil {
		t.Fatal(err)
	}
	task := model.Task{ID: "director-at-finalize", ProjectID: project.ID, UserID: project.UserID, Type: "canvas_text", Status: model.TaskStatusRunning,
		InputJSON: testJSON(t, map[string]any{"metadata": map[string]string{"source": "short-drama-chapter-storyboard", "domainProjectId": project.ID, "chapterId": unit.ID}})}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	unlock := lockOntologyEpisode(unit.ID)
	result := make(chan error, 1)
	go func() { result <- s.DeleteProjectUnit(project.UserID, project.ID, unit.ID) }()
	select {
	case err := <-result:
		unlock()
		var conflict *AppError
		if !errors.As(err, &conflict) || conflict.Status != http.StatusConflict || conflict.Reason != kernel.ReasonEpisodeActiveTasks || !strings.Contains(conflict.Message, task.ID) {
			t.Fatalf("expected active director conflict, got %v", err)
		}
	case <-time.After(5 * time.Second):
		unlock()
		<-result
		t.Fatal("deletion waited for the director mutex instead of reporting its active task")
	}
}
