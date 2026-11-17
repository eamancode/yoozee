package app

import (
	"encoding/json"
	"testing"
	"time"

	"yingce/backend/internal/model"
)

func TestVideoReceiptDestination(t *testing.T) {
	s, db := newDramaWorkerTestService(t)
	original := model.ModelChannel{ID: "dest-channel", Scope: model.ChannelScopeSystem, Enabled: true, BaseURL: "https://ORIGINAL.example:443/v1"}
	if err := db.Create(&original).Error; err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Add(time.Second)
	task := model.Task{ID: "dest-task", Type: "canvas_video", UserID: "user-1", BillingOrderID: "dest-order", RouteRun: 1, CreatedAt: started}
	input := map[string]any{"config": map[string]any{"channelId": original.ID}, videoReceiptOriginKey: "client-supplied"}
	if err := s.stampVideoReceiptDestination(&task, input); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	task.InputJSON = string(encoded)
	moved := original
	moved.BaseURL, moved.UpdatedAt = "https://replacement.example/api", started.Add(time.Hour)
	check := func(name string, candidate model.Task, channel model.ModelChannel, allowed bool) {
		t.Helper()
		err := s.validateVideoReceiptDestination(&candidate, &channel, "original-job", started)
		if (err == nil) != allowed {
			t.Fatalf("%s: allowed=%v err=%v", name, allowed, err)
		}
	}
	current := original
	current.BaseURL = "https://original.example"
	current.UpdatedAt = moved.UpdatedAt // credential rotation after admission
	check("same origin and new credentials", task, current, true)
	check("new host after admission", task, moved, false)
	other := task
	other.ID = "copied-task"
	check("copied origin proof", other, current, false)
	legacy := task
	legacy.InputJSON = `{"config":{"channelId":"dest-channel","model":"old"}}`
	check("unchanged historical channel row", legacy, original, true)
	check("unverifiable historical channel change", legacy, moved, false)
	log := model.ApiCallLog{
		ID: "actual-create", Source: "backend-task", Capability: "video", RequestKind: "create", Status: model.ApiCallStatusSucceeded,
		TaskID: task.ID, UserID: task.UserID, ChannelID: original.ID, BillingOrderID: task.BillingOrderID, ProviderRequestID: "original-job",
		StartedAt: started, CreatedAt: started.Add(time.Second), UpstreamURL: "https://original.example/v1/video/generations",
	}
	if err := db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	check("historical create receipt after credential rotation", legacy, current, true)
	check("create receipt disagrees with new destination", legacy, moved, false)
	conflicting := log
	conflicting.ID = "conflicting-create"
	conflicting.UpstreamURL = "https://replacement.example/api/generations"
	if err := db.Create(&conflicting).Error; err != nil {
		t.Fatal(err)
	}
	check("conflicting origins for one job", task, current, false)
}
