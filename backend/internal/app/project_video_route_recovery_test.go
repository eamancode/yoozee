package app

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectVideoRoutingRecoveredAttemptClearsStreakOnce(t *testing.T) {
	f := newProjectRoutingFixture(t)
	const channel = "original"
	record := func(task *model.Task) {
		t.Helper()
		if err := f.s.recordProjectVideoTerminal(task); err != nil {
			t.Fatal(err)
		}
	}
	first := routingReceipt(t, f, f.projectID, channel, 0, model.TaskStatusFailed)
	record(first)
	requireRoutingStreak(t, f, f.projectID, channel, 1)
	stale := *first
	first.Status = model.TaskStatusSucceeded
	record(first)
	requireRoutingStreak(t, f, f.projectID, channel, 0)
	record(routingReceipt(t, f, f.projectID, channel, 0, model.TaskStatusFailed))
	record(first)
	record(&stale)
	record(routingReceipt(t, f, f.projectID, channel, 0, model.TaskStatusFailed))
	requireRoutingStreak(t, f, f.projectID, channel, 2)
	var receipts []model.ProjectVideoRouteEvent
	if err := f.db.Where("task_id = ? AND route_run = ?", first.ID, first.RouteRun).Find(&receipts).Error; err != nil {
		t.Fatal(err)
	}
	route, err := f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || route != nil || len(receipts) != 1 || receipts[0].Decision != "reset" {
		t.Fatalf("reconciled attempt produced a duplicate receipt or premature switch: receipts=%+v route=%+v err=%v", receipts, route, err)
	}
}
