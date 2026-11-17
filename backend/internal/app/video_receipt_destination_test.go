package app

import (
	"encoding/hex"
	"encoding/json"
	"strings"
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
	other = task
	other.RouteRun++
	check("copied proof from an earlier attempt", other, current, false)
	otherChannel := current
	otherChannel.ID = "another-channel"
	check("origin proof from another channel", task, otherChannel, false)
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

func TestVideoReceiptDestinationAdmissionScope(t *testing.T) {
	f := newProjectRoutingFixture(t)
	if _, err := f.s.OverrideProjectVideoRouting("user-1", f.projectID, routingOriginal, "", 0); err != nil {
		t.Fatal(err)
	}
	chain := f.start(t)
	state := f.tick(t, chain.ID)
	step := state.Steps[0]
	parent, err := f.s.repo.TaskForUser("user-1", chain.ID)
	if err != nil {
		t.Fatal(err)
	}
	input, err := f.s.videoChainInput(parent, &step)
	if err != nil {
		t.Fatal(err)
	}
	input[videoReceiptOriginKey] = "client-supplied"
	request := CreateTaskRequest{
		ProjectID: f.projectID, Type: "canvas_video", Operation: "text_to_video",
		Prompt: stringValue(input["prompt"]), Model: step.Model, Input: input,
		admission:       &taskAdmission{ID: step.TaskID, MaxCharge: &step.Quote.AmountMicrocredits, ParentControllerID: chain.ID, ParentControllerLease: parent.LeaseOwner},
		creationPrepare: &creationTaskPreparation{},
	}
	prepared, err := f.s.CreateTask(parent.UserID, request)
	if err != nil || strings.Contains(prepared.InputJSON, videoReceiptOriginKey) {
		t.Fatalf("chain preflight must not carry a receipt origin: %v", err)
	}
	state = f.untilTask(t, chain.ID, 0)
	child, err := f.s.repo.TaskForUser(parent.UserID, state.Steps[0].TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Stamp videoReceiptOriginStamp `json:"_videoReceiptOrigin"`
	}
	if err := json.Unmarshal([]byte(child.InputJSON), &saved); err != nil {
		t.Fatal(err)
	}
	channel, err := f.s.repo.SystemChannel("system-video")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := videoReceiptOrigin(channel.BaseURL)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.s.videoReceiptOriginMAC(child, channel.ID, origin)
	if err != nil || saved.Stamp.ChannelID != channel.ID || saved.Stamp.RouteRun != child.RouteRun || saved.Stamp.MAC != hex.EncodeToString(proof) {
		t.Fatalf("persisted chain child is missing its own signed channel origin: %v", err)
	}

	ordinary := globalSixSecondsRequest("false")
	ordinary["config"].(map[string]any)["videoSeconds"] = "30"
	ordinary[videoReceiptOriginKey] = saved.Stamp
	task, err := f.s.CreateTask(parent.UserID, CreateTaskRequest{
		Type: "canvas_video", Operation: "text_to_video", ProjectID: f.projectID,
		Prompt: "普通画布视频", Input: ordinary,
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, err := f.s.repo.TaskForUser(parent.UserID, task.ID)
	if err != nil || strings.Contains(stored.InputJSON, videoReceiptOriginKey) {
		t.Fatalf("ordinary canvas task persisted a video-chain origin proof: %v", err)
	}
}
