package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

type wireRequest struct {
	Path        string
	ContentType string
	Body        map[string]any
}

// 第五轮：资产出图的最终上游请求（不是另建 providerConfig 只验参数换算）。走真实链路：资产出图排队 → 建任务准入（资产画幅改写、
// 三处尺寸字段）→ 系统渠道模型匹配与报价（ChannelModel 的能力配置、价格档）→ worker 组 provider 配置 → 发到假上游；
// 在假上游抓实际序列化的请求体逐项核对。电影 16:9、短剧 9:16 两个项目各出三张：微缩场景母版、人物、道具。
func TestAssetImageFinalUpstreamPayload(t *testing.T) {
	for _, c := range []struct{ workflow, ratio string }{{drama.WorkflowSSSFilm, "16:9"}, {drama.WorkflowSSSDrama, "9:16"}} {
		t.Run(c.workflow, func(t *testing.T) {
			svc, db := newDramaWorkerTestService(t)
			project, _ := seedEpisodeProject(t, svc, db)
			if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Updates(map[string]any{"workflow_id": c.workflow, "aspect_ratio": c.ratio}).Error; err != nil {
				t.Fatal(err)
			}
			folderID, err := svc.resolveProjectAssetFolderID(project.ID, nil)
			if err != nil {
				t.Fatal(err)
			}
			frame := map[string]string{"16:9": "landscape 16:9", "9:16": "portrait 9:16"}[c.ratio]
			base := time.Now()
			for i, a := range []struct {
				title    string
				category model.AssetCategory
			}{{"夜市雨棚", model.AssetCategoryEnvironment}, {"保温杯", model.AssetCategoryProp}, {"林深", model.AssetCategoryCharacter}} {
				now := base.Add(time.Duration(i) * time.Second)
				assetID, versionID := "wire-asset-"+strconv.Itoa(i), "wire-version-"+strconv.Itoa(i)
				definition, _ := json.Marshal(map[string]any{"prompt": frame + " " + a.title + "，雨夜", "description": a.title, "appearance": a.title + "的外观"})
				payload, _ := json.Marshal(map[string]any{"id": assetID, "kind": "entity", "category": a.category, "status": model.AssetVersionStatusConfirmed, "primaryVersionId": versionID, "title": a.title, "metadata": map[string]any{assetImageKey: dramaImageState{Status: dramaImageNone}}})
				for _, row := range []any{
					&model.Asset{ID: assetID, UserID: "user-1", Kind: "entity", Category: a.category, Status: model.AssetVersionStatusConfirmed, PrimaryVersionID: versionID, Title: a.title, PayloadJSON: string(payload), CreatedAt: now, UpdatedAt: now},
					&model.AssetVersion{ID: versionID, AssetID: assetID, Version: 1, Status: model.AssetVersionStatusConfirmed, DefinitionJSON: string(definition), CreatedAt: now, UpdatedAt: now},
					&model.ProjectAssetLink{ID: "wire-link-" + strconv.Itoa(i), ProjectID: project.ID, AssetID: assetID, FolderID: folderID, Position: i, CreatedAt: now},
				} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			var mu sync.Mutex
			var captured []wireRequest
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				request := wireRequest{Path: r.Method + " " + r.URL.Path, ContentType: r.Header.Get("Content-Type")}
				if err := json.Unmarshal(raw, &request.Body); err != nil {
					t.Errorf("upstream body is not JSON (%s): %.200s", request.ContentType, raw)
				}
				mu.Lock()
				captured = append(captured, request)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				// 1×1 的方图：用来看收尾的比例核对（方形母版相符，竖版人物不符）。
				_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"b64_json": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="}}})
			}))
			t.Cleanup(server.Close)
			// 系统渠道的图片模型：能力配置给出 1:1 / 9:16 / 16:9 的像素预设，固定价格档。
			profile := DefaultModelCapabilityConfigForModel("openai-image", "wire-image")
			profile.Image.Size.Presets = []ImageSizePreset{{Tier: "1k", Ratio: "9:16", Width: 864, Height: 1536, Size: "864x1536"}, {Tier: "1k", Ratio: "1:1", Width: 1024, Height: 1024, Size: "1024x1024"}, {Tier: "1k", Ratio: "16:9", Width: 1536, Height: 864, Size: "1536x864"}}
			capability, _ := json.Marshal(profile)
			for _, row := range []any{
				&model.ModelChannel{ID: "system-image", Scope: model.ChannelScopeSystem, Enabled: true, Name: "Mock system image", BaseURL: server.URL, APIKey: "fixture-only"},
				&model.ChannelModel{ID: "system-image-model", ChannelID: "system-image", ModelKey: "wire-image", ProviderModelKey: "wire-image-upstream", Capability: "image", Protocol: model.ChannelInterfaceType("openai-image"), CapabilityConfigJSON: string(capability), Enabled: true, PriceConfigured: true, BillingMode: "fixed_request"},
				&model.ChannelModelPriceTier{ID: "system-image-tier", ChannelModelID: "system-image-model", SelectorKey: "{}", SelectorJSON: "{}", ProviderModelKey: "wire-image-upstream", BillingMode: "fixed_request", UnitPriceMicrocredits: 1_000_000, Enabled: true, PriceConfigured: true},
				&model.User{ID: "user-1", Username: "creator", DisplayName: "创作者", Role: model.UserRoleUser},
				&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 50_000_000},
			} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			// 模板和前端一样带项目画幅（资产自己的画幅由准入改写，不靠模板）。
			template := DramaImageTaskTemplate{Model: "wire-image", Input: map[string]any{"mode": "image", "prompt": "资产出图", "config": map[string]any{"channelId": "system-image", "model": "wire-image", "size": c.ratio, "count": "1"}}}
			queued, err := svc.QueueDramaAssetImages("user-1", project.ID, DramaAssetImagesRequest{ImageTask: &template})
			if err != nil || len(queued.Queued) != 3 {
				t.Fatalf("queue: %+v %v", queued, err)
			}
			results := map[string]map[string]any{}
			for range 3 {
				done, runErr := runQueuedTask(t, svc)
				if runErr != nil || done.Status != model.TaskStatusSucceeded {
					t.Fatalf("task %s: %s %q %v", done.ID, done.Status, done.Error, runErr)
				}
				var result map[string]any
				_ = json.Unmarshal([]byte(done.ResultJSON), &result)
				results[done.ID] = result
			}
			if len(captured) != 3 {
				t.Fatalf("upstream requests = %d", len(captured))
			}
			byAsset := map[string]wireRequest{}
			for _, request := range captured {
				prompt, _ := request.Body["prompt"].(string)
				for _, title := range []string{"夜市雨棚", "保温杯", "林深"} {
					if strings.Contains(prompt, "【当前资产】"+title) {
						byAsset[title] = request
					}
				}
				// 模型匹配：发出去的是渠道模型的上游名；没有参考图时走生成接口（JSON），不带任何图片字段。
				if request.Body["model"] != "wire-image-upstream" || request.Path != "POST /v1/images/generations" || !strings.HasPrefix(request.ContentType, "application/json") {
					t.Fatalf("model / endpoint: %s %s %v", request.Path, request.ContentType, request.Body["model"])
				}
				for _, key := range []string{"image", "images", "image_url", "reference_images", "mask"} {
					if _, ok := request.Body[key]; ok {
						t.Fatalf("request carries reference field %q", key)
					}
				}
				if strings.Contains(prompt, frame) {
					t.Fatalf("project frame %q leaked into the final prompt", frame)
				}
			}
			master, prop, person := byAsset["夜市雨棚"], byAsset["保温杯"], byAsset["林深"]
			if master.Body["size"] != "1024x1024" || !strings.HasSuffix(master.Body["prompt"].(string), drama.SpatialMasterOutputNote) {
				t.Fatalf("spatial master wire: size=%v prompt tail=%q", master.Body["size"], tail(master.Body["prompt"]))
			}
			if person.Body["size"] != "864x1536" || !strings.Contains(person.Body["prompt"].(string), "竖版9:16") {
				t.Fatalf("character wire: size=%v prompt tail=%q", person.Body["size"], tail(person.Body["prompt"]))
			}
			if !strings.HasSuffix(prop.Body["prompt"].(string), drama.PropBodyOutputNote("保温杯")) || !strings.Contains(prop.Body["prompt"].(string), "只呈现「保温杯」这一件道具本体") {
				t.Fatalf("prop wire: prompt tail=%q", tail(prop.Body["prompt"]))
			}
			// 收尾比例核对：返回 1×1 方图，方形母版相符，竖版人物不符（只记录告警，不裁切、不重出，任务照样成功）。
			var masterAspect, personAspect map[string]any
			for _, result := range results {
				aspect, _ := result["imageAspect"].(map[string]any)
				switch aspect["requested"] {
				case "1:1":
					masterAspect = aspect
				case "9:16":
					personAspect = aspect
				}
			}
			if masterAspect == nil || masterAspect["matches"] != true || personAspect == nil || personAspect["matches"] != false {
				t.Fatalf("aspect checks: master=%v person=%v", masterAspect, personAspect)
			}
		})
	}
}

func tail(prompt any) string {
	text, _ := prompt.(string)
	runes := []rune(text)
	return string(runes[max(0, len(runes)-120):])
}
