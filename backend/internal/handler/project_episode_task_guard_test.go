package handler

import (
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
	"yingce/backend/internal/repository"
)

const episodeGuardURL = "/api/projects/guard-project"
const episodeDirectorInput = `{"metadata":{"domainProjectId":"guard-project","source":"short-drama-chapter-storyboard","chapterId":"middle","dramaSSS":{}}}`

func episodeGuardFixture(t *testing.T) (*gorm.DB, func(string, string, string) *httptest.ResponseRecorder) {
	t.Helper()
	// 剧集写操作与模型插件无关；在隔离的空目录初始化插件，仍使用真实 service 和数据库。
	t.Setenv("CANVAS_OFFICIAL_PLUGIN_DIR", t.TempDir())
	router, db, cookie := profileHandlerFixture(t)
	RegisterProjectRoutes(router.Group("/api"), runtimeService)
	if err := db.Exec("PRAGMA journal_mode=WAL").Error; err != nil {
		t.Fatal(err)
	}
	for _, row := range []any{
		&model.Project{ID: "guard-project", UserID: "profile-user", Status: model.ProjectStatusActive, GenerationModel: "seedance-2.5"},
		&model.CanvasProject{ID: "guard-canvas", ProjectID: "guard-project", UserID: "profile-user", PayloadJSON: `{"nodes":[]}`},
		&model.ProjectUnit{ID: "first", ProjectID: "guard-project", Kind: model.ProjectUnitKindEpisode, Title: "首集", Position: 0},
		&model.ProjectUnit{ID: "middle", ProjectID: "guard-project", Kind: model.ProjectUnitKindEpisode, Title: "中集", Position: 1, SourceText: "门外雨声"},
		&model.ProjectUnit{ID: "last", ProjectID: "guard-project", Kind: model.ProjectUnitKindEpisode, Title: "末集", Position: 2},
		&model.Shot{ID: "shot-m", ProjectID: "guard-project", UnitID: "middle", CurrentRevisionID: "rev-m"},
		&model.Shot{ID: "shot-l", ProjectID: "guard-project", UnitID: "last", CurrentRevisionID: "rev-l"},
		&model.ShotRevision{ID: "rev-m", ShotID: "shot-m", Version: 1, Dialogue: "雨停了吗？"},
		&model.ShotRevision{ID: "rev-l", ShotID: "shot-l", Version: 1, Dialogue: "停了。"},
		&model.ShotAssetReference{ID: "ref-m", ShotID: "shot-m", AssetVersionID: "prop-v1", Status: "linked"},
		&model.ShotArtifact{ID: "take-m", ProjectID: "guard-project", UnitID: "middle", ShotID: "shot-m", RevisionID: "rev-m", ResourceID: "media-m", Type: "video", Selected: true, Version: 1},
		&model.ShotEvidence{ID: "evidence-m", ProjectID: "guard-project", UnitID: "middle", ShotID: "shot-m", ArtifactID: "take-m"},
		&model.DramaShotlist{ID: "shotlist-m", ProjectID: "guard-project", UnitID: "middle", ShotlistJSON: `{"title":"雨夜"}`},
		&model.ProjectOntologyDocument{ID: "handoff-m", ProjectID: "guard-project", UnitID: "middle", Kind: model.OntologyDocHandoff, Version: 1, Content: "雨伞留在门外", TaskID: "doc-writer"},
		&model.CanvasUnitLink{ID: "canvas-m", ProjectID: "guard-project", CanvasID: "guard-canvas", UnitID: "middle"},
		&model.WorkflowInstance{ID: "flow-m", ProjectID: "guard-project", UnitID: "middle"},
		&model.WorkflowStepInstance{ID: "step-m", WorkflowInstanceID: "flow-m", StepKey: "shots"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, func(method, path, body string) *httptest.ResponseRecorder {
		return profileRequest(router, method, episodeGuardURL+path, body, cookie)
	}
}

type episodeGuardTask struct {
	ID, Type, Status string
}

func assertEpisodeConflict(t *testing.T, rec *httptest.ResponseRecorder, want map[string]episodeGuardTask) {
	t.Helper()
	var envelope struct {
		Code    int
		Reason  string
		Msg     string
		Details struct {
			UnitID string
			Tasks  []episodeGuardTask
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil || rec.Code != http.StatusConflict || envelope.Code == 0 ||
		envelope.Reason != "episode_active_tasks" || envelope.Details.UnitID != "middle" || !strings.Contains(envelope.Msg, "重试") {
		t.Fatalf("DELETE conflict: %d %s", rec.Code, rec.Body.String())
	}
	got := map[string]episodeGuardTask{}
	for _, task := range envelope.Details.Tasks {
		got[task.ID] = task
		if !strings.Contains(envelope.Msg, task.ID) {
			t.Fatalf("conflict message omits task %q: %s", task.ID, envelope.Msg)
		}
	}
	if len(envelope.Details.Tasks) != len(want) || !maps.Equal(got, want) || strings.Contains(rec.Body.String(), "fixture-private-input") {
		t.Fatalf("incorrect or unsafe blocker details: %s; want %v", rec.Body.String(), want)
	}
}

func TestProjectEpisodeDeleteActiveTasksHTTP(t *testing.T) {
	db, request := episodeGuardFixture(t)
	const direct = `{"metadata":{"domainProjectId":"guard-project","shotId":"shot-m","private":"fixture-private-input"}}`
	const chain = `{"unitId":"middle","steps":[{"shotId":"shot-m"}]}`
	const other = `{"metadata":{"domainProjectId":"guard-project","unitId":"last","shotId":"shot-l"}}`
	tasks := []model.Task{
		{ID: "queued-chain", Type: model.TaskTypeProjectVideoChain, Status: model.TaskStatusQueued, InputJSON: chain},
		{ID: "cancelled-parent", Type: model.TaskTypeProjectVideoChain, Status: model.TaskStatusCancelled, InputJSON: chain},
		{ID: "running-child", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: `{"_projectVideoChain":{"controllerId":"cancelled-parent"}}`},
		{ID: "whole-episode-director", Type: "canvas_text", Status: model.TaskStatusQueued, InputJSON: episodeDirectorInput},
		{ID: "doc-writer", Type: "canvas_text", Status: model.TaskStatusTextReplay, InputJSON: "{}"},
		{ID: "workflow-task", Type: "canvas_image", Status: model.TaskStatusQueued, InputJSON: "{}"},
		{ID: "pending-linked", ProjectID: "guard-canvas", Type: "canvas_image", Status: "pending", InputJSON: "{}"},
		{ID: "legacy-canvas-shot", ProjectID: "guard-canvas", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: direct},
		{ID: "other-episode", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: other},
		{ID: "other-owner", UserID: "another-user", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: direct},
		{ID: "other-project", ProjectID: "another-project", Type: "canvas_video", Status: model.TaskStatusRunning, InputJSON: direct},
		{ID: "archived-take", Type: "canvas_video", Status: model.TaskStatusSucceeded, InputJSON: direct},
		{ID: "failed-take", Type: "canvas_video", Status: model.TaskStatusFailed, InputJSON: direct},
		{ID: "linked-render", Type: "timeline_render", Status: model.TaskStatusRunning, InputJSON: "{}"},
		{ID: "unlinked-render", Type: "timeline_render", Status: model.TaskStatusRunning, InputJSON: `{"title":"middle"}`},
	}
	want := map[string]episodeGuardTask{}
	for i := range tasks {
		task := &tasks[i]
		if task.ProjectID == "" {
			task.ProjectID = "guard-project"
		}
		if task.UserID == "" {
			task.UserID = "profile-user"
		}
		if err := db.Create(task).Error; err != nil {
			t.Fatal(err)
		}
		switch task.ID {
		case "queued-chain", "running-child", "whole-episode-director", "doc-writer", "workflow-task", "pending-linked", "legacy-canvas-shot", "linked-render":
			want[task.ID] = episodeGuardTask{task.ID, task.Type, string(task.Status)}
		}
	}
	for _, row := range []any{
		&model.ProductionTaskLink{ID: "link-pending", TaskID: "pending-linked", ProjectID: "guard-project", UnitID: "middle"},
		&model.ProductionTaskLink{ID: "link-render", TaskID: "linked-render", ProjectID: "guard-project", UnitID: "middle", ShotID: "shot-m"},
		&model.WorkflowStepTask{ID: "step-link", TaskID: "workflow-task", WorkflowStepID: "step-m"},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	before := episodeStateSnapshot(t, db)
	assertEpisodeConflict(t, request("DELETE", "/units/middle", ""), want)
	if before != episodeStateSnapshot(t, db) {
		t.Fatal("rejected DELETE changed project contents or task state")
	}
	if err := db.Model(&model.Task{}).Where("id IN ?", slices.Collect(maps.Keys(want))).Update("status", model.TaskStatusSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if rec := request("DELETE", "/units/middle", ""); rec.Code != http.StatusOK {
		t.Fatalf("unlinked tasks, other episodes and terminal tasks must allow delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := request("GET", "/units/last", ""); rec.Code != http.StatusOK {
		t.Fatalf("unrelated episode: %d %s", rec.Code, rec.Body.String())
	}
	for _, id := range []string{"other-episode", "other-owner", "other-project", "unlinked-render"} {
		var active model.Task
		if err := db.First(&active, "id = ?", id).Error; err != nil || active.Status != model.TaskStatusRunning {
			t.Fatalf("unrelated task %s changed: status=%s err=%v", id, active.Status, err)
		}
	}
}

// 项目 revision、排序、正文、版本、运行记录、定版产物与画布都逐字段比对。
func episodeStateSnapshot(t *testing.T, db *gorm.DB) string {
	t.Helper()
	var all [][]map[string]any
	for _, table := range []any{
		&model.Project{}, &model.ProjectUnit{}, &model.Shot{}, &model.ShotRevision{}, &model.ShotAssetReference{},
		&model.ShotArtifact{}, &model.ShotEvidence{}, &model.ProjectOntologyDocument{}, &model.DramaShotlist{},
		&model.CanvasProject{}, &model.CanvasUnitLink{}, &model.WorkflowInstance{}, &model.WorkflowStepInstance{},
		&model.WorkflowStepTask{}, &model.Task{}, &model.ProductionTaskLink{},
	} {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(table); err != nil {
			t.Fatal(err)
		}
		var rows []map[string]any
		if err := db.Table(stmt.Schema.Table).Order("id").Find(&rows).Error; err != nil {
			t.Fatal(err)
		}
		all = append(all, rows)
	}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func waitEpisodeGuardBarrier(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for episode transaction barrier")
	}
}

// 两个请求都已开始，先后顺序由实际 SQL 边界决定，无需 sleep，也不用执行任何模型。
func TestProjectEpisodeDeleteAgainstStaleShotHTTP(t *testing.T) {
	db, request := episodeGuardFixture(t)
	read, release := make(chan struct{}), make(chan struct{})
	resume := sync.OnceFunc(func() { close(release) })
	defer resume()
	var paused atomic.Bool
	if err := db.Callback().Query().After("gorm:query").Register("test:hold-read-episode", func(tx *gorm.DB) {
		unit, ok := tx.Statement.Dest.(*model.ProjectUnit)
		if ok && unit.ID == "middle" && paused.CompareAndSwap(false, true) {
			close(read)
			<-release
		}
	}); err != nil {
		t.Fatal(err)
	}
	result := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		result <- request("POST", "/shots", `{"unitId":"middle","title":"增加分镜","description":"雨声渐起","durationMs":1000}`)
	}()
	waitEpisodeGuardBarrier(t, read)
	removed := request("DELETE", "/units/middle", "")
	resume()
	created := <-result
	if removed.Code != http.StatusOK || (created.Code != http.StatusNotFound && created.Code != http.StatusConflict) {
		t.Fatalf("DELETE %d %s; POST with stale unit %d %s", removed.Code, removed.Body.String(), created.Code, created.Body.String())
	}
	var orphanShots, orphanRevisions int64
	if err := db.Model(&model.Shot{}).Where("unit_id = ?", "middle").Count(&orphanShots).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.ShotRevision{}).Where("shot_id NOT IN (?)", db.Model(&model.Shot{}).Select("id")).Count(&orphanRevisions).Error; err != nil {
		t.Fatal(err)
	}
	if orphanShots != 0 || orphanRevisions != 0 {
		t.Fatalf("orphan shots %d, revisions %d", orphanShots, orphanRevisions)
	}
}

func TestProjectEpisodeDeleteAgainstDirectorAdmissionHTTP(t *testing.T) {
	for _, admissionFirst := range []bool{false, true} {
		name := "delete-commits-first"
		if admissionFirst {
			name = "admission-commits-first"
		}
		t.Run(name, func(t *testing.T) {
			db, request := episodeGuardFixture(t)
			task := model.Task{ID: "concurrent-director", ProjectID: "guard-project", UserID: "profile-user", Type: "canvas_text", Status: model.TaskStatusQueued, InputJSON: episodeDirectorInput}
			repo := repository.New(db)
			admitted := make(chan error, 1)
			ready, release := make(chan struct{}), make(chan struct{})
			resume := sync.OnceFunc(func() { close(release) })
			defer resume()
			var paused atomic.Bool
			if admissionFirst {
				deleting := make(chan struct{})
				var inserting atomic.Bool
				if err := db.Callback().Create().After("gorm:create").Register("test:hold-admitted-task", func(tx *gorm.DB) {
					if tx.Statement.Table == "tasks" && inserting.CompareAndSwap(false, true) {
						close(ready)
						<-release
					}
				}); err != nil {
					t.Fatal(err)
				}
				if err := db.Callback().Update().Before("gorm:update").Register("test:delete-entering-lock", func(tx *gorm.DB) {
					if tx.Statement.Table == "projects" && inserting.Load() && paused.CompareAndSwap(false, true) {
						close(deleting)
					}
				}); err != nil {
					t.Fatal(err)
				}
				go func() { admitted <- repo.CreateTaskWithActiveLimit(&task, 1) }()
				select {
				case <-ready:
				case err := <-admitted:
					t.Fatalf("director admission completed before insertion barrier: %v", err)
				case <-time.After(10 * time.Second):
					t.Fatal("director admission did not reach insertion barrier")
				}
				rec := make(chan *httptest.ResponseRecorder, 1)
				go func() { rec <- request("DELETE", "/units/middle", "") }()
				waitEpisodeGuardBarrier(t, deleting)
				resume()
				if err := <-admitted; err != nil {
					t.Fatal(err)
				}
				assertEpisodeConflict(t, <-rec, map[string]episodeGuardTask{task.ID: {task.ID, task.Type, string(task.Status)}})
				if rec := request("GET", "/units/middle", ""); rec.Code != http.StatusOK {
					t.Fatalf("episode disappeared under admitted director: %s", rec.Body.String())
				}
				return
			}
			if err := db.Callback().Update().Before("gorm:update").Register("test:hold-director-admission", func(tx *gorm.DB) {
				if tx.Statement.Table == "projects" && paused.CompareAndSwap(false, true) {
					close(ready)
					<-release
				}
			}); err != nil {
				t.Fatal(err)
			}
			go func() { admitted <- repo.CreateTaskWithActiveLimit(&task, 1) }()
			waitEpisodeGuardBarrier(t, ready)
			removed := request("DELETE", "/units/middle", "")
			resume()
			if err := <-admitted; !errors.Is(err, repository.ErrStoryboardTaskUnitGone) || removed.Code != http.StatusOK {
				t.Fatalf("late director admission err=%v, DELETE %d %s", err, removed.Code, removed.Body.String())
			}
			var tasks int64
			if err := db.Model(&model.Task{}).Where("id = ?", task.ID).Count(&tasks).Error; err != nil || tasks != 0 {
				t.Fatalf("orphan director count=%d err=%v", tasks, err)
			}
		})
	}
}
