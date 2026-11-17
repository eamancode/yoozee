package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

var archiveEpisodeWrites = []struct{ method, path, body string }{
	{"POST", "/units", `{"kind":"episode","title":"补集","sourceText":"<p>新剧本</p>"}`},
	{"PATCH", "/units/middle", `{"title":"中集新标题","sourceText":"<p>新台词</p>"}`},
	{"DELETE", "/units/middle", ""},
}

func checkArchivedEpisodeResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var envelope struct {
		Code        int
		Reason, Msg string
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != http.StatusBadRequest ||
		envelope.Code == 0 || envelope.Reason != "invalid_argument" || !strings.Contains(envelope.Msg, "已归档") {
		t.Errorf("archived episode write: HTTP %d %s", response.Code, response.Body.String())
	}
}

func TestProjectEpisodeArchiveHTTP(t *testing.T) {
	db, request := episodeGuardFixture(t)
	// 活跃项目先走同三个写入口，校验返回的稳定 ID、正文与实际 revision。
	var initial model.Project
	if err := db.First(&initial, "id = ?", "guard-project").Error; err != nil {
		t.Fatal(err)
	}
	created := request("POST", "/units", archiveEpisodeWrites[0].body)
	var data struct {
		Data struct{ Unit model.ProjectUnit }
	}
	if err := json.Unmarshal(created.Body.Bytes(), &data); err != nil || created.Code != http.StatusOK || data.Data.Unit.ID == "" || data.Data.Unit.SourceText != "<p>新剧本</p>" {
		t.Fatalf("active create: %d %s", created.Code, created.Body.String())
	}
	unitID := data.Data.Unit.ID
	updated := request("PATCH", "/units/"+unitID, archiveEpisodeWrites[1].body)
	if err := json.Unmarshal(updated.Body.Bytes(), &data); err != nil || updated.Code != http.StatusOK || data.Data.Unit.ID != unitID || data.Data.Unit.Title != "中集新标题" || data.Data.Unit.SourceText != "<p>新台词</p>" {
		t.Fatalf("active update: %d %s", updated.Code, updated.Body.String())
	}
	if rec := request("DELETE", "/units/"+unitID, ""); rec.Code != http.StatusOK {
		t.Fatalf("active delete: %d %s", rec.Code, rec.Body.String())
	}
	var active model.Project
	if err := db.First(&active, "id = ?", initial.ID).Error; err != nil || active.Revision != initial.Revision+3 {
		t.Fatalf("active writes revision=%d, initial=%d, err=%v", active.Revision, initial.Revision, err)
	}
	var tasks int64
	if err := db.Model(&model.Task{}).Count(&tasks).Error; err != nil || tasks != 0 {
		t.Fatalf("active episode writes created tasks=%d err=%v", tasks, err)
	}
	if rec := request("PATCH", "", `{"status":"archived"}`); rec.Code != http.StatusOK {
		t.Fatalf("archive project: %d %s", rec.Code, rec.Body.String())
	}
	before := episodeStateSnapshot(t, db)
	for _, write := range archiveEpisodeWrites {
		t.Run(write.method, func(t *testing.T) {
			checkArchivedEpisodeResponse(t, request(write.method, write.path, write.body))
			if episodeStateSnapshot(t, db) != before {
				t.Error("archived mutation changed project, episode, associated content, or tasks")
			}
		})
	}
	if rec := request("GET", "/units/middle", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "门外雨声") {
		t.Fatalf("read archived episode: %d %s", rec.Code, rec.Body.String())
	}
}

func TestProjectEpisodeArchiveDuringMutationHTTP(t *testing.T) {
	for _, write := range archiveEpisodeWrites {
		t.Run(write.method, func(t *testing.T) {
			db, request := episodeGuardFixture(t)
			read, release := make(chan struct{}), make(chan struct{})
			var paused atomic.Bool
			resume := sync.OnceFunc(func() { close(release) })
			defer resume()
			// 已读到 active，尚未进入剧集写事务。在同一项目提交实际归档请求。
			if err := db.Callback().Query().After("gorm:query").Register("test:archive-after-episode-preflight", func(tx *gorm.DB) {
				project, ok := tx.Statement.Dest.(*model.Project)
				if ok && project.ID == "guard-project" && project.Status == model.ProjectStatusActive && paused.CompareAndSwap(false, true) {
					close(read)
					<-release
				}
			}); err != nil {
				t.Fatal(err)
			}
			result := make(chan *httptest.ResponseRecorder, 1)
			go func() { result <- request(write.method, write.path, write.body) }()
			waitEpisodeGuardBarrier(t, read)
			archived := request("PATCH", "", `{"status":"archived"}`)
			if archived.Code != http.StatusOK {
				t.Fatalf("concurrent archive: %d %s", archived.Code, archived.Body.String())
			}
			before := episodeStateSnapshot(t, db)
			resume()
			checkArchivedEpisodeResponse(t, <-result)
			if before != episodeStateSnapshot(t, db) {
				t.Fatal("in-flight episode write changed committed archived state")
			}
		})
	}
}
