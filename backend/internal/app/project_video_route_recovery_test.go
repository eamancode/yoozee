package app

import (
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectVideoRoutingReselectAfterFailoverStartsNewStreak(t *testing.T) {
	f := newProjectRoutingFixture(t)
	addRoutingAlternative(t, f, "backup", 2_000_000, model.TaskStatusSucceeded)
	fail := func(revision int64) {
		t.Helper()
		task := routingReceipt(t, f, f.projectID, "system-video", revision, model.TaskStatusFailed)
		if err := f.s.recordProjectVideoTerminal(task); err != nil {
			t.Fatal(err)
		}
	}
	selected := func(status, wantModel string, revision int64) {
		t.Helper()
		route, err := f.s.ProjectVideoRouting("user-1", f.projectID)
		if err != nil || route == nil || route.Status != status || route.Model != wantModel || route.Revision != revision {
			t.Fatalf("want %s %s revision %d, got %+v err=%v", status, wantModel, revision, route, err)
		}
	}
	const backup = "backup::seedance-2.5-test"
	fail(0)
	fail(0)
	fail(0)
	selected("auto", backup, 1)
	if _, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, routingOriginal, backup, 1); err != nil {
		t.Fatal(err)
	}
	fail(2)
	fail(0)
	requireRoutingStreak(t, f, f.projectID, "system-video", 1)
	fail(2)
	selected("manual", routingOriginal, 2)
	fail(2)
	selected("auto", backup, 3)
}

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
