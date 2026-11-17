package handler

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yingce/backend/internal/auth"
	"yingce/backend/internal/model"
	"yingce/backend/internal/service"
)

func TestPoseLibraryHTTPUseAndCharacterRepresentations(t *testing.T) {
	router, db := newCanvasOperationsHTTPTest(t)
	admin := model.User{ID: "admin", Username: "pose-admin", Role: model.UserRoleAdmin, Status: model.UserStatusActive, Email: "admin@example.test"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	adminSession := model.AuthSession{ID: "admin-session", UserID: admin.ID, TokenHash: auth.HashToken("test-token"), ExpiresAt: time.Now().Add(time.Hour)}
	if err := db.Create(&adminSession).Error; err != nil {
		t.Fatal(err)
	}
	const userCookie = "session.test-token"
	const adminCookie = "admin-session.test-token"
	call := func(method, path, cookie, contentType string, body []byte, want int) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, "/api"+path, bytes.NewReader(body))
		if cookie != "" {
			req.AddCookie(&http.Cookie{Name: service.SessionCookieName, Value: cookie})
		}
		req.Header.Set("Content-Type", contentType)
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		if out.Code != want {
			t.Fatalf("%s %s: HTTP %d, want %d: %s", method, path, out.Code, want, out.Body.String())
		}
		return out
	}
	readData := func(out *httptest.ResponseRecorder, target any) {
		t.Helper()
		var envelope struct {
			Code int
			Data json.RawMessage
		}
		if err := json.Unmarshal(out.Body.Bytes(), &envelope); err != nil || envelope.Code != 0 {
			t.Fatalf("unexpected envelope: %s", out.Body.String())
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			t.Fatal(err)
		}
	}
	webp, err := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAkAAEAcQERGIiP4HAA==")
	if err != nil {
		t.Fatal(err)
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	if err := w.WriteField("name", "测试姿势"); err != nil {
		t.Fatal(err)
	}
	file, err := w.CreateFormFile("file", "pose.webp")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write(webp); err != nil {
		t.Fatal(err)
	}
	if err = w.Close(); err != nil {
		t.Fatal(err)
	}
	call("GET", "/pose-library/poses", "", "", nil, http.StatusUnauthorized)
	call("POST", "/admin/pose-library/items", userCookie, w.FormDataContentType(), form.Bytes(), http.StatusForbidden)
	var created struct{ Item struct{ ID string } }
	readData(call("POST", "/admin/pose-library/items", adminCookie, w.FormDataContentType(), form.Bytes(), 200), &created)
	if created.Item.ID == "" {
		t.Fatal("missing pose ID")
	}
	catalog := call("GET", "/pose-library/poses", userCookie, "", nil, 200)
	if !strings.Contains(catalog.Body.String(), created.Item.ID) {
		t.Fatalf("published pose missing from catalog: %s", catalog.Body.String())
	}
	for _, variant := range []string{"thumb", "image"} {
		out := call("GET", "/pose-library/items/"+created.Item.ID+"/"+variant, userCookie, "", nil, 200)
		cfg, format, err := image.DecodeConfig(out.Body)
		if err != nil || cfg.Width != 3 || cfg.Height != 2 || (variant == "thumb" && format != "jpeg") || (variant == "image" && format != "webp") {
			t.Fatalf("pose %s: %s %+v %v", variant, format, cfg, err)
		}
	}
	useBody, _ := json.Marshal(map[string][]string{"ids": {created.Item.ID}})
	resourceID := ""
	for range 2 {
		var result struct {
			Items []struct{ Resource model.Resource }
		}
		readData(call("POST", "/pose-library/use", userCookie, "application/json", useBody, 200), &result)
		if len(result.Items) != 1 || result.Items[0].Resource.UserID != "user" || result.Items[0].Resource.ID == "" {
			t.Fatalf("unexpected imported pose: %+v", result)
		}
		if resourceID != "" && result.Items[0].Resource.ID != resourceID {
			t.Fatal("repeat pose selection created a duplicate resource")
		}
		resourceID = result.Items[0].Resource.ID
	}
	path := "/resources/" + resourceID + "/file?format=jpeg"
	out := call("GET", path, userCookie, "", nil, 200)
	pic, err := jpeg.Decode(out.Body)
	if err != nil || pic.Bounds().Dx() != 3 || pic.Bounds().Dy() != 2 || out.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("invalid transcoded JPEG: %v", err)
	}
	r, g, b, _ := pic.At(0, 0).RGBA()
	if min(r, g, b) < 0xf000 {
		t.Fatal("transparent pixels must be flattened against white")
	}
	call("GET", path, adminCookie, "", nil, http.StatusNotFound)
	var character service.CharacterAssetSummary
	readData(call("POST", "/characters", userCookie, "application/json", []byte(`{"name":"林深","definition":{"appearance":"旧设定","clothing":"西装"}}`), 200), &character)
	representations := []map[string]string{{"role": "primary", "resourceId": resourceID}, {"role": "outfit:work", "resourceId": resourceID}}
	replacePath := "/characters/" + character.Asset.ID + "/representations"
	update, _ := json.Marshal(map[string]any{"representations": representations, "definition": map[string]any{"appearance": "新设定", "clothing": "西装", "outfits": []string{"work"}}})
	readData(call("PUT", replacePath, userCookie, "application/json", update, 200), &character)
	onlyImages, _ := json.Marshal(map[string]any{"representations": representations})
	readData(call("PUT", replacePath, userCookie, "application/json", onlyImages, 200), &character)
	readData(call("GET", "/characters/"+character.Asset.ID, userCookie, "", nil, 200), &character)
	definition := character.Character.Definition
	if definition["appearance"] != "新设定" || definition["clothing"] != "西装" || definition["outfits"] == nil || len(character.Character.Representations) != 2 {
		t.Fatalf("lost definition or representations: %+v", character.Character)
	}
	var total int64
	if err := db.Model(&model.Task{}).Count(&total).Error; err != nil || total != 0 {
		t.Fatalf("unexpected model jobs: %d (%v)", total, err)
	}
}

func TestImageTranscodeRejectsExcessPixelCount(t *testing.T) {
	var compressed bytes.Buffer
	if err := png.Encode(&compressed, image.NewGray(image.Rect(0, 0, 4096, 4096))); err != nil {
		t.Fatal(err)
	}
	if _, err := transcodeImageToJPEG(&compressed); err == nil {
		t.Fatal("accepted an image over the pixel limit")
	}
}
