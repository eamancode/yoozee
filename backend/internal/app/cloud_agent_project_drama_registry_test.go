package app

import "testing"

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
