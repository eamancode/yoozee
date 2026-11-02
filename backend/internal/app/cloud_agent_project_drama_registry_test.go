package app

import (
	"strings"
	"testing"

	"yingce/backend/internal/model"
)

func TestProjectDramaRegistryKeepsToolsScoped(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  CloudAgentRequest
		want int
	}{
		{"plain", CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}}, 0},
		{"upstream drama", CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, DramaLinked: true}, 0},
		{"project", CloudAgentRequest{PermissionMode: "auto", ContextScope: []string{"canvas"}, DramaProjectID: "project-1"}, 18},
		{"project read only", CloudAgentRequest{PermissionMode: "read_only", ContextScope: []string{"canvas"}, DramaProjectID: "project-1"}, 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := 0
			for _, tool := range cloudAgentTools(tc.req) {
				name := tool["function"].(map[string]any)["name"].(string)
				if cloudAgentDramaTool(name) {
					count++
				}
			}
			if count != tc.want {
				t.Fatalf("got %d project drama tools, want %d", count, tc.want)
			}
		})
	}
	if cloudAgentDramaTool("drama_get_context") || cloudAgentDramaTool("drama_adopt_canvas_media") {
		t.Fatal("upstream drama tool routed into project workbench")
	}
}

func TestProjectDramaWriteWaitsForApproval(t *testing.T) {
	for _, decision := range []string{"approve", "reject"} {
		t.Run(decision, func(t *testing.T) {
			s, db, args := agentMediaFixture(t)
			project := model.Project{ID: "drama-approval", UserID: "user", Type: "short-drama", Status: model.ProjectStatusActive, Name: "审批验收", BriefJSON: `{}`}
			if err := db.Create(&project).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CanvasProject{}).Where("id = ?", "agent-canvas").Update("project_id", project.ID).Error; err != nil {
				t.Fatal(err)
			}
			run, state := agentMediaRun(t, s, args, "request_approval")
			state.Request.DramaProjectID = project.ID
			call := cloudAgentCall{ID: "write-brief"}
			call.Function.Name = "drama_brief_write"
			call.Function.Arguments = `{"document":{"title":"approved-only"}}`
			state.Calls, state.CallIndex = []cloudAgentCall{call}, 0
			if err := s.advanceCloudAgentTool(run, &state); err != nil {
				t.Fatal(err)
			}
			current, err := s.repo.ProjectForUser("user", project.ID)
			if err != nil || current.BriefJSON != `{}` {
				t.Fatalf("wrote before approval: %+v %v", current, err)
			}
			run, err = s.repo.CloudAgent("user", run.ID)
			if err != nil {
				t.Fatal(err)
			}
			state, err = cloudAgentDecode(run)
			if err != nil || run.Status != "waiting_approval" || state.Approval == nil || state.Approval.Preview.Kind != "project_drama" {
				t.Fatalf("missing project approval: %s %v", run.Status, err)
			}
			s.disablePiRuntime = true
			if err := s.DecideCloudAgentApproval("user", run.ID, state.Approval.ID, decision, ""); err != nil {
				t.Fatal(err)
			}
			current, err = s.repo.ProjectForUser("user", project.ID)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(current.BriefJSON, "approved-only") != (decision == "approve") {
				t.Fatalf("decision %s: %s", decision, current.BriefJSON)
			}
		})
	}
}
