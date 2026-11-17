package app

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"yingce/backend/internal/model"
)

const budgetRouteChannel = "alternative-budget"

// Exercise the same automatically selected tier at quote, confirmation and worker admission.
func newAutoRouteBudgetFixture(t *testing.T) *videoChainFixture {
	t.Helper()
	f := newProjectRoutingFixture(t)
	addRoutingAlternative(t, f, budgetRouteChannel, 8_000_000, model.TaskStatusSucceeded)
	for range 3 {
		receipt := routingReceipt(t, f, f.projectID, "system-video", 0, model.TaskStatusFailed)
		if err := f.s.recordProjectVideoTerminal(receipt); err != nil {
			t.Fatal(err)
		}
	}
	route, err := f.s.ProjectVideoRouting("user-1", f.projectID)
	if err != nil || route == nil || route.Status != "auto" || route.Model != budgetRouteChannel+"::seedance-2.5-test" {
		t.Fatalf("automatic selection: %+v, %v", route, err)
	}
	f.req.Model.Model = route.Model
	f.req.Model.Input["config"].(map[string]any)["channelId"] = budgetRouteChannel
	return f
}

func changeBudgetRouteTier(t *testing.T, f *videoChainFixture, field string, value any) {
	t.Helper()
	result := f.db.Model(&model.ChannelModelPriceTier{}).Where("id = ?", budgetRouteChannel+"-30s").Update(field, value)
	if result.Error != nil || result.RowsAffected != 1 {
		t.Fatalf("update matched tier: %d %v", result.RowsAffected, result.Error)
	}
}

func budgetRouteQuote(t *testing.T, f *videoChainFixture, cost int64) ProjectVideoChainView {
	t.Helper()
	view, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
	if err != nil || len(view.Steps) != 1 || view.TotalMicrocredits != 8_000_000 || view.Steps[0].Quote == nil {
		t.Fatalf("automatic route quote: %+v, %v", view, err)
	}
	upstream := view.Steps[0].Quote.UpstreamCost
	if upstream == nil || upstream.AmountMicros != cost || upstream.Unit != "request" || upstream.Currency != "CNY" {
		t.Fatalf("supplier quote: %+v, want %d micro-CNY per request", upstream, cost)
	}
	return view
}

func TestProjectVideoAutoRouteRequotesSupplierCost(t *testing.T) {
	f := newAutoRouteBudgetFixture(t)
	initial := budgetRouteQuote(t, f, 4_000_000)
	changeBudgetRouteTier(t, f, "upstream_request_cost_micro_cny", 5_000_000)
	fresh := budgetRouteQuote(t, f, 5_000_000)
	if initial.PreviewHash == fresh.PreviewHash {
		t.Fatal("supplier cost changed but the confirmed quote identifier did not")
	}
	f.req.PreviewHash = initial.PreviewHash
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req); err == nil {
		t.Fatal("accepted the previous supplier quote")
	}
	f.req.PreviewHash = fresh.PreviewHash
	if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req); err != nil {
		t.Fatalf("fresh affordable quote could not be confirmed: %v", err)
	}
}

func TestProjectVideoAutoRouteRejectsIneligibleTier(t *testing.T) {
	for _, condition := range []string{"6.01 CNY", "unknown cost", "disabled tier", "per-second tier", "disabled channel", "shortened duration", "group excludes channel"} {
		t.Run(condition, func(t *testing.T) {
			f := newAutoRouteBudgetFixture(t)
			quote := budgetRouteQuote(t, f, 4_000_000)
			switch condition {
			case "6.01 CNY":
				changeBudgetRouteTier(t, f, "upstream_request_cost_micro_cny", 6_010_000)
			case "unknown cost":
				changeBudgetRouteTier(t, f, "upstream_request_cost_micro_cny", nil)
			case "disabled tier":
				changeBudgetRouteTier(t, f, "enabled", false)
			case "per-second tier":
				changeBudgetRouteTier(t, f, "billing_mode", "per_second")
			case "disabled channel":
				if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", budgetRouteChannel).Update("enabled", false).Error; err != nil {
					t.Fatal(err)
				}
			case "shortened duration":
				profile := DefaultModelCapabilityConfigForModel("seedance-videos-compatible", "seedance-2.5-test")
				profile.Video.Duration = VideoDurationConfig{Selection: "enum", Values: []int{15}, Default: 15}
				raw := mustEncodeModelCapabilityConfig(t, profile)
				if err := f.db.Model(&model.ChannelModel{}).Where("channel_id = ?", budgetRouteChannel).Update("capability_config_json", raw).Error; err != nil {
					t.Fatal(err)
				}
			case "group excludes channel":
				group := model.UserGroup{ID: "budget-restrict", Code: "original-only", ModelOverridesJSON: "[]"}
				for _, row := range []any{&group, &model.UserGroupChannel{GroupID: group.ID, ChannelID: "system-video"}} {
					if err := f.db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := f.db.Model(&model.User{}).Where("id = ?", "user-1").Update("user_group", group.Code).Error; err != nil {
					t.Fatal(err)
				}
				f.s.invalidateUserGroups()
			}
			if _, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req); err == nil {
				t.Fatalf("quoted an ineligible automatic route after %s", condition)
			}
			f.req.PreviewHash = quote.PreviewHash
			if _, err := f.s.CreateProjectVideoChain("user-1", f.projectID, f.req); err == nil {
				t.Fatal("accepted stale quote")
			}
			var orders int64
			if err := f.db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil || orders != 0 {
				t.Fatalf("ineligible selection reserved %d orders: %v", orders, err)
			}
			current, err := f.s.ProjectVideoRouting("user-1", f.projectID)
			if err != nil || current == nil {
				t.Fatalf("current project route: %+v, %v", current, err)
			}
			if _, err = f.s.OverrideProjectVideoRouting("user-1", f.projectID, routingOriginal, current.Model, current.Revision); err != nil {
				t.Fatalf("could not select an available replacement: %v", err)
			}
			f.req.Model.Model = routingOriginal
			f.req.Model.Input["config"].(map[string]any)["channelId"] = "system-video"
			f.req.PreviewHash = ""
			fresh, err := f.s.PreviewProjectVideoChain("user-1", f.projectID, f.req)
			if err != nil || len(fresh.Steps) != 1 || fresh.Steps[0].Model != routingOriginal || fresh.PreviewHash == quote.PreviewHash {
				t.Fatalf("manual replacement quote: %+v, %v", fresh, err)
			}
		})
	}
}

func TestProjectVideoAutoRouteControllerAndChildRecheckCost(t *testing.T) {
	t.Run("confirmed controller before child admission", func(t *testing.T) {
		f := newAutoRouteBudgetFixture(t)
		chain := f.start(t)
		changeBudgetRouteTier(t, f, "upstream_request_cost_micro_cny", 5_000_000)
		state := f.tick(t, chain.ID)
		if state.Steps[0].Status != "failed" {
			t.Fatalf("controller did not stop at changed quotation: %+v", state.Steps[0])
		}
		var children, orders int64
		if err := f.db.Model(&model.Task{}).Where("id = ?", state.Steps[0].TaskID).Count(&children).Error; err != nil || children != 0 {
			t.Fatalf("controller created %d paid children: %v", children, err)
		}
		if err := f.db.Model(&model.BillingOrder{}).Count(&orders).Error; err != nil || orders != 0 {
			t.Fatalf("controller reserved %d orders: %v", orders, err)
		}
	})
	t.Run("queued child before supplier POST", func(t *testing.T) {
		f := newAutoRouteBudgetFixture(t)
		var posts atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				posts.Add(1)
			}
			http.Error(w, "unexpected provider call", http.StatusBadRequest)
		}))
		t.Cleanup(provider.Close)
		if err := f.db.Model(&model.ModelChannel{}).Where("id = ?", budgetRouteChannel).Update("base_url", provider.URL).Error; err != nil {
			t.Fatal(err)
		}
		chain := f.start(t)
		state := f.untilTask(t, chain.ID, 0)
		changeBudgetRouteTier(t, f, "upstream_request_cost_micro_cny", 5_000_000)
		child, workerErr := runQueuedTask(t, f.s)
		if child.ID != state.Steps[0].TaskID || workerErr == nil || child.Status != model.TaskStatusFailed || posts.Load() != 0 {
			t.Fatalf("queued child %s status=%s worker error=%v provider POSTs=%d", child.ID, child.Status, workerErr, posts.Load())
		}
		var order model.BillingOrder
		if err := f.db.Where("task_id = ?", child.ID).Take(&order).Error; err != nil || order.Status != model.BillingStatusRefunded || order.ActualAmountMicrocredits != 0 {
			t.Fatalf("uncharged child billing status=%s actual=%d: %v", order.Status, order.ActualAmountMicrocredits, err)
		}
	})
}
