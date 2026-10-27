package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

const styleLockReply = `{"style_anchor": {"medium": "真人实拍", "art_style": "都市冷调悬疑", "lighting": "实景光源，冷暖分区", "camera": "定焦，克制", "grading": "低饱和冷调", "forbidden": ["霓虹", "HDR"]}}
{"group": "主角组", "colors": [{"name_zh": "护士蓝", "name_en": "Nurse Blue", "hex": "#5B7FA3", "usage": "林小满制服"}, {"name_zh": "苍白肤", "name_en": "Pale Skin", "hex": "#E3D3C4", "usage": "主角肤色"}, {"name_zh": "墨黑发", "name_en": "Ink Black", "hex": "#1B1B1F", "usage": "主角发色"}]}
{"group": "配角组", "colors": [{"name_zh": "病号条纹", "name_en": "Patient Stripe", "hex": "#8DA1B9", "usage": "周岸病号服"}, {"name_zh": "黑衣", "name_en": "Night Coat", "hex": "#26262B", "usage": "黑衣人"}]}
{"group": "道具组", "colors": [{"name_zh": "铁锈红", "name_en": "Rust Red", "hex": "#7A3B2E", "usage": "七床床牌"}, {"name_zh": "刀光银", "name_en": "Blade Silver", "hex": "#B8BEC4", "usage": "刀"}]}
{"group": "环境组", "colors": [{"name_zh": "日光灯白", "name_en": "Fluorescent White", "hex": "#E8ECEF", "usage": "走廊顶灯"}, {"name_zh": "水泥灰", "name_en": "Concrete Grey", "hex": "#6E6E6E", "usage": "墙面"}, {"name_zh": "应急绿", "name_en": "Exit Green", "hex": "#2F7D5B", "usage": "安全出口"}]}`

// visionUpstream 是能收图的假文本上游：记下每次请求最后一条消息的内容（文字 + 图片），回固定的风格锁定输出。
type visionUpstream struct {
	mu     sync.Mutex
	calls  int
	texts  []string
	images []string
	reply  string
}

func (u *visionUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	u.mu.Lock()
	u.calls++
	if n := len(body.Messages); n > 0 {
		var parts []struct {
			Type     string `json:"type"`
			Text     string `json:"text"`
			ImageURL struct {
				URL string `json:"url"`
			} `json:"image_url"`
		}
		var text string
		if json.Unmarshal(body.Messages[n-1].Content, &parts) == nil {
			for _, p := range parts {
				if p.Type == "image_url" {
					u.images = append(u.images, p.ImageURL.URL)
				} else {
					text += p.Text
				}
			}
		} else {
			_ = json.Unmarshal(body.Messages[n-1].Content, &text)
		}
		u.texts = append(u.texts, text)
	}
	u.mu.Unlock()
	reply, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": u.reply}, "finish_reason": "stop"}}})
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(reply)
}

func testPNG(t *testing.T, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for x := 0; x < 8; x++ {
		for y := 0; y < 8; y++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func uploadTestImage(t *testing.T, svc *Service, userID string, c color.Color) string {
	t.Helper()
	data := testPNG(t, c)
	resource, err := svc.UploadResourceFile(userID, "ref.png", int64(len(data)), "image", 8, 8, 0, bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return resource.ID
}

func queueStyleLockTask(t *testing.T, svc *Service, db *gorm.DB, projectID string, upstream string, req DramaProjectStepRunRequest) *model.Task {
	t.Helper()
	step, _ := drama.StepByID(drama.StepStyleLock)
	current, err := svc.repo.ProjectForUser("user-1", projectID)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := svc.planStyleLock("user-1", current, step, req)
	if err != nil {
		t.Fatal(err)
	}
	taskReq := dramaStepTaskRequest(projectID, "", plan, dramaStepTaskOptions{Model: "test", Config: map[string]any{"baseUrl": upstream, "apiKey": "test-key", "model": "test", "interfaceType": "chat-completion"}})
	taskReq.Input["metadata"].(map[string]any)["dramaRun"] = newDramaRunMeta(plan, "")
	return insertDramaTestTask(t, svc, db, projectID, taskReq)
}

// 建项目时传了参考图和风格文字：风格分析一轮，图随消息发给模型；收尾只出候选，项目不动；锁定后写 bible、合成主色卡图、建主色卡资产，
// 「风格定了」成立；存进风格库后另一个账号能看到、选用（不跑分析），不能删；管理员能删。
func TestStyleLockAnalysisLockAndLibrary(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedNovelProject(t, svc, db)
	upstream := &visionUpstream{reply: styleLockReply}
	server := httptest.NewServer(upstream)
	defer server.Close()

	refs := []string{uploadTestImage(t, svc, "user-1", color.RGBA{0x5B, 0x7F, 0xA3, 0xFF}), uploadTestImage(t, svc, "user-1", color.RGBA{0xE8, 0xEC, 0xEF, 0xFF})}
	queueStyleLockTask(t, svc, db, project.ID, server.URL, DramaProjectStepRunRequest{ProjectType: "live-action", StyleText: "都市冷调悬疑，少用霓虹", ReferenceImages: refs})
	done, err := runQueuedTask(t, svc)
	if err != nil || done.Status != model.TaskStatusSucceeded {
		t.Fatalf("风格分析：%v %s %q", err, done.Status, done.Error)
	}
	if upstream.calls != 1 || len(upstream.images) != 2 || !strings.HasPrefix(upstream.images[0], "data:image/png;base64,") {
		t.Fatalf("一轮、两张参考图以图片发给模型：%d %d %.40v", upstream.calls, len(upstream.images), upstream.images)
	}
	for _, want := range []string{"【项目类型】仿真人", "都市冷调悬疑，少用霓虹", "附了 2 张参考图", "【受保护输出契约】"} {
		if !strings.Contains(upstream.texts[0], want) {
			t.Fatalf("提示词缺「%s」", want)
		}
	}
	var result struct {
		DramaStep DramaStyleLockOutcome `json:"dramaStep"`
	}
	if err := json.Unmarshal([]byte(done.ResultJSON), &result); err != nil {
		t.Fatal(err)
	}
	outcome := result.DramaStep
	if outcome.Status != DramaStyleLockOutcomeCandidate || outcome.Source != drama.StyleLockSourceAnalysis || outcome.ProjectType != "live-action" || len(outcome.MasterPalette.Colors()) != 10 || len(outcome.MasterPalette.Groups) != 4 || outcome.StyleAnchor.Medium != "真人实拍" {
		t.Fatalf("候选：%+v", outcome)
	}
	if stored, _ := svc.repo.ProjectForUser("user-1", project.ID); strings.Contains(stored.BibleJSON, "master_palette") {
		t.Fatal("收尾只出候选，不写项目")
	}

	locked, err := svc.LockDramaStyle("user-1", project.ID, DramaStyleLockRequest{TaskID: done.ID})
	if err != nil {
		t.Fatal(err)
	}
	if locked.StyleLock.Source != drama.StyleLockSourceAnalysis || locked.StyleLock.TaskID != done.ID || locked.StyleLock.AssetID == "" || locked.StyleLock.ResourceID == "" || locked.ImageError != "" {
		t.Fatalf("锁定：%+v", locked)
	}
	if locked.Asset == nil || locked.Asset.MediaType != "image" || locked.Asset.Palette == nil || locked.Asset.Palette.Master == nil || len(locked.Asset.Palette.Master.Palette.Colors()) != 10 || locked.Asset.Palette.Code != "" {
		t.Fatalf("主色卡资产：%+v", locked.Asset)
	}
	resource, err := svc.repo.ResourceForUser("user-1", locked.StyleLock.ResourceID)
	if err != nil || resource.Width != drama.MasterPaletteImageWidth || resource.Height != drama.MasterPaletteImageHeight || resource.MimeType != "image/png" {
		t.Fatalf("合成图：%v %+v", err, resource)
	}
	bible, err := svc.ProjectBible("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bible["style_anchor"].(map[string]any)["art_style"] != "都市冷调悬疑" || len(bible["master_palette"].(map[string]any)["groups"].([]any)) != 4 || bible["style_lock"].(map[string]any)["project_type"] != "live-action" {
		t.Fatalf("bible：%+v", bible)
	}
	progress, err := svc.DramaProgress("user-1", project.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range progress.EndStates {
		if e.Key == "style" && (!e.Done || !strings.Contains(e.Detail, "10 色主基调色卡")) {
			t.Fatalf("风格定了：%+v", e)
		}
	}
	// 场次色卡的路径看不到主色卡：出图队列、「色卡齐了」都只认有场编号的。
	if palettes, err := svc.projectPaletteAssets("user-1", project.ID); err != nil || len(palettes) != 0 {
		t.Fatalf("主色卡不算场次色卡：%v %d", err, len(palettes))
	}
	// 再锁一次：主色卡加版本，不多建资产。
	if _, err := svc.LockDramaStyle("user-1", project.ID, DramaStyleLockRequest{TaskID: done.ID}); err != nil {
		t.Fatal(err)
	}
	all, err := svc.FilterProjectAssets("user-1", project.ID, ProjectAssetFilter{Category: string(model.AssetCategoryPalette)})
	if err != nil || len(all) != 1 {
		t.Fatalf("重锁不多建主色卡：%v %d", err, len(all))
	}

	owner := &model.User{ID: "user-1", Role: "user"}
	item, err := svc.SaveDramaStyleToLibrary(owner, DramaStyleLibrarySaveRequest{ProjectID: project.ID, Name: " 都市冷调悬疑 "})
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "都市冷调悬疑" || item.ProjectType != "live-action" || item.ColorCount != 10 || item.CoverURL == "" || !item.CanEdit || item.Source != DramaStyleLibrarySourceAnalysis {
		t.Fatalf("存进风格库：%+v", item)
	}
	if _, err := svc.SaveDramaStyleToLibrary(owner, DramaStyleLibrarySaveRequest{ProjectID: project.ID, Name: "都市冷调悬疑"}); err == nil || !strings.Contains(err.Error(), "已经有叫") {
		t.Fatalf("同类型重名：%v", err)
	}
	if bible, _ := svc.ProjectBible("user-1", project.ID); bible["style_lock"].(map[string]any)["library_id"] != item.ID {
		t.Fatalf("项目记下存进的条目：%+v", bible["style_lock"])
	}

	// 另一个账号：风格库里看得到（不能改删），建项目选它直接带上风格锚和主色卡，不跑分析。
	other := &model.User{ID: "user-2", Role: "user"}
	list, err := svc.DramaStyleLibrary(other, "live-action")
	if err != nil || len(list.Items) != 1 || list.Items[0].CanEdit || len(list.ProjectTypes) != 4 {
		t.Fatalf("团队共享：%v %+v", err, list)
	}
	if empty, _ := svc.DramaStyleLibrary(other, "2d-anime"); len(empty.Items) != 0 {
		t.Fatalf("按类型归档：%+v", empty.Items)
	}
	delivery, err := svc.PrepareDramaStyleLibraryCoverDelivery(other, item.ID, ResourceAccessOptions{}, "")
	if err != nil || delivery.Resource.UserID != "user-1" {
		t.Fatalf("别的账号读得到封面（资源属于存入者）：%v", err)
	}
	if delivery.Stream != nil {
		_ = delivery.Stream.Body.Close()
	}
	theirs, err := svc.CreateProject("user-2", CreateProjectRequest{Name: "另一部", GenerationModel: "seedance-2.5"})
	if err != nil {
		t.Fatal(err)
	}
	var before int64
	db.Model(&model.Task{}).Count(&before)
	picked, err := svc.LockDramaStyle("user-2", theirs.ID, DramaStyleLockRequest{LibraryID: item.ID})
	if err != nil {
		t.Fatal(err)
	}
	var after int64
	db.Model(&model.Task{}).Count(&after)
	if picked.StyleLock.Source != drama.StyleLockSourceLibrary || picked.StyleLock.LibraryID != item.ID || picked.StyleAnchor.ArtStyle != "都市冷调悬疑" || after != before || picked.Asset == nil || picked.Asset.MediaType != "image" {
		t.Fatalf("选用风格库：%+v（任务 %d → %d）", picked, before, after)
	}
	if res, err := svc.repo.ResourceForUser("user-2", picked.StyleLock.ResourceID); err != nil || res.UserID != "user-2" {
		t.Fatalf("选用时主色卡图在自己账号下重新合成：%v", err)
	}
	if _, err := svc.SaveDramaStyleToLibrary(other, DramaStyleLibrarySaveRequest{ProjectID: theirs.ID, Name: "再存一次"}); err == nil {
		t.Fatal("取自风格库的风格不用再存")
	}
	var forbidden *AuthError
	if err := svc.DeleteDramaStyleLibraryEntry(other, item.ID); !errors.As(err, &forbidden) || forbidden.Status != http.StatusForbidden {
		t.Fatalf("普通用户不能删别人的：%v", err)
	}
	newName := "冷调悬疑"
	if _, err := svc.UpdateDramaStyleLibraryEntry(other, item.ID, DramaStyleLibraryUpdateRequest{Name: &newName}); !errors.As(err, &forbidden) {
		t.Fatalf("普通用户不能改别人的：%v", err)
	}
	renamed, err := svc.UpdateDramaStyleLibraryEntry(owner, item.ID, DramaStyleLibraryUpdateRequest{Name: &newName})
	if err != nil || renamed.Name != newName {
		t.Fatalf("创建者能改名：%v %+v", err, renamed)
	}
	if err := svc.DeleteDramaStyleLibraryEntry(&model.User{ID: "admin", Role: model.UserRoleAdmin}, item.ID); err != nil {
		t.Fatalf("管理员能删：%v", err)
	}
	// 条目删了，已选用的项目保留自己复制的风格。
	if _, _, _, ok := projectLockedStyle(mustProjectBible(t, svc, "user-2", theirs.ID)); !ok {
		t.Fatal("删了风格库条目，已选用的项目不受影响")
	}
}

func mustProjectBible(t *testing.T, svc *Service, userID, projectID string) string {
	t.Helper()
	p, err := svc.repo.ProjectForUser(userID, projectID)
	if err != nil {
		t.Fatal(err)
	}
	return p.BibleJSON
}

// 风格分析的输入校验：类型、参考图归属与张数；没有文字、没有图、也没有画风时不跑；只有画风时按画风生成（source=preset）。
func TestPlanStyleLockValidates(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedNovelProject(t, svc, db)
	step, _ := drama.StepByID(drama.StepStyleLock)
	plan := func(p *model.Project, req DramaProjectStepRunRequest) (*dramaStepPlan, error) {
		return svc.planStyleLock("user-1", p, step, req)
	}
	if _, err := plan(&project, DramaProjectStepRunRequest{ProjectType: "anime"}); err == nil || !strings.Contains(err.Error(), "项目类型") {
		t.Fatalf("类型：%v", err)
	}
	theirs := uploadTestImage(t, svc, "user-2", color.White)
	if _, err := plan(&project, DramaProjectStepRunRequest{ProjectType: "live-action", ReferenceImages: []string{theirs}}); err == nil || !strings.Contains(err.Error(), "不属于当前账号") {
		t.Fatalf("别人的图：%v", err)
	}
	var many []string
	for i := 0; i < drama.StyleLockMaxImages+1; i++ {
		many = append(many, fmt.Sprintf("r%d", i))
	}
	if _, err := plan(&project, DramaProjectStepRunRequest{ProjectType: "live-action", ReferenceImages: many}); err == nil || !strings.Contains(err.Error(), "最多") {
		t.Fatalf("张数：%v", err)
	}
	preset, err := plan(&project, DramaProjectStepRunRequest{ProjectType: "live-action"})
	if err != nil || preset.lock.Source != drama.StyleLockSourcePreset || !strings.Contains(preset.prompt.Text, "按【项目画风】") {
		t.Fatalf("只按画风：%v %+v", err, preset)
	}
	blank, err := svc.CreateProject("user-1", CreateProjectRequest{Name: "空白", GenerationModel: "seedance-2.5"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan(&blank, DramaProjectStepRunRequest{ProjectType: "live-action"}); err == nil || !strings.Contains(err.Error(), "风格描述") {
		t.Fatalf("什么都没有：%v", err)
	}
}

// 剧本优化的提示词带上项目锁定的风格锚；没有锁定时不写这一段、照常能跑。
func TestScriptAdaptPromptCarriesLockedAnchor(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, _ := seedNovelProject(t, svc, db)
	step, _ := drama.StepByID(drama.StepScriptAdapt)
	plain, err := svc.planScriptAdapt(&project, step, DramaProjectStepRunRequest{TargetEpisodes: 1})
	if err != nil || strings.Contains(plain.prompt.Text, "【项目风格锚】") {
		t.Fatalf("没锁定时不写：%v", err)
	}
	bible := `{"style_anchor":{"medium":"真人实拍","forbidden":["霓虹"]},"master_palette":{"groups":[{"key":"protagonist","name":"主角组","colors":[{"name_zh":"护士蓝","name_en":"Nurse Blue","hex":"#5B7FA3","usage":"制服"}]}]},"style_lock":{"source":"library"}}`
	if err := db.Model(&model.Project{}).Where("id = ?", project.ID).Update("bible_json", bible).Error; err != nil {
		t.Fatal(err)
	}
	project.BibleJSON = bible
	locked, err := svc.planScriptAdapt(&project, step, DramaProjectStepRunRequest{TargetEpisodes: 1})
	if err != nil || !strings.Contains(locked.prompt.Text, "【项目风格锚】") || !strings.Contains(locked.prompt.Text, "- 禁用：霓虹") {
		t.Fatalf("锁定后带上风格锚：%v", err)
	}
}
