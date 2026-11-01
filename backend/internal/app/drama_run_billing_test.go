package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// 逐轮续写（按小节生成 4 节）一个任务一张计费单：结算按 4 次成功调用的用量逐次计价再相加，不是只算最后一轮；
// 第 2 次调用上游 502 没有用量、重试成功，失败的那次不计费。按次计费的模型 4 轮 = 4 次。
// 钱包只有一笔 consume，金额等于任务摘要的实扣。
func TestDramaRunBillingSettlesEveryCall(t *testing.T) {
	// opus 线上单价：输入 5、缓存 2、输出 10 µ/token（每百万 Token 的价格配置）。每次调用：输入、缓存、输出。
	usages := [][3]int64{{1000, 0, 200}, {1500, 800, 300}, {2100, 1500, 250}, {2700, 2100, 400}}
	// 逐次：7000、8100、8500、11200；只算最后一轮是 11200。
	const tokenTotal = int64(7000 + 8100 + 8500 + 11200)
	for _, tc := range []struct {
		name      string
		order     map[string]any
		reserved  int64
		want      int64
		wantUsage bool
	}{
		{name: "按 Token", reserved: 100, want: tokenTotal, wantUsage: true, order: map[string]any{
			"billing_mode": "token", "capability": "text", "multiplier_basis_points": 10_000,
			"input_token_price_microcredits": 5_000_000, "cached_token_price_microcredits": 2_000_000, "output_token_price_microcredits": 10_000_000,
		}},
		// 建单时按 4 轮放大了预授权：Quantity 4、金额 4 × 300000。
		{name: "按次", reserved: 1_200_000, want: 1_200_000, order: map[string]any{
			"billing_mode": "fixed_request", "capability": "text", "multiplier_basis_points": 10_000, "unit_price_microcredits": 300_000, "quantity": 4,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, db := newDramaWorkerTestService(t)
			project, unit := seedLongQichuang(t, svc, db, 2, 4)
			upstream := &sectionShotsUpstream{
				fail: func(call int, _ string) int {
					if call == 2 {
						return http.StatusBadGateway
					}
					return 0
				},
				usage: func(call int) map[string]any {
					index := call - 1
					if call > 2 {
						index = call - 2
					}
					u := usages[index]
					return map[string]any{"prompt_tokens": u[0], "completion_tokens": u[2], "prompt_tokens_details": map[string]any{"cached_tokens": u[1]}}
				},
			}
			server := httptest.NewServer(upstream)
			defer server.Close()
			task := queueDramaStepTask(t, svc, db, project, unit, server.URL, "")
			if shots := taskDramaShots(task.InputJSON); shots == nil || len(shots.Sections) != 4 {
				t.Fatalf("2 场每场 4 句、文戏 15 两句一节＝4 节：%+v", shots)
			}
			updates := map[string]any{"amount_microcredits": tc.reserved, "reserved_amount_microcredits": tc.reserved}
			for key, value := range tc.order {
				updates[key] = value
			}
			if err := db.Model(&model.BillingOrder{}).Where("id = ?", task.BillingOrderID).Updates(updates).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.CreditAccount{}).Where("user_id = ?", "user-1").Updates(map[string]any{"available_microcredits": 10_000_000, "reserved_microcredits": tc.reserved}).Error; err != nil {
				t.Fatal(err)
			}

			done, err := runQueuedTask(t, svc)
			if err != nil || done.Status != model.TaskStatusSucceeded {
				t.Fatalf("4 节写完：%v %s %q", err, done.Status, done.Error)
			}
			if upstream.calls != 5 {
				t.Fatalf("4 节 + 1 次 502 重试＝5 次调用：%d", upstream.calls)
			}
			order, err := svc.repo.BillingOrder(task.BillingOrderID)
			if err != nil {
				t.Fatal(err)
			}
			if order.Status != model.BillingStatusSettled || order.ActualAmountMicrocredits != tc.want {
				t.Fatalf("结算＝4 次成功调用之和 %d：%s %d", tc.want, order.Status, order.ActualAmountMicrocredits)
			}
			if tc.wantUsage && (order.InputTokens != 7300 || order.CachedTokens != 4400 || order.OutputTokens != 1150) {
				t.Fatalf("订单用量是 4 次合计：%d / %d / %d", order.InputTokens, order.CachedTokens, order.OutputTokens)
			}
			if !tc.wantUsage && order.Quantity != 4 {
				t.Fatalf("按次结算的次数：%d", order.Quantity)
			}
			var consumes []model.CreditLedgerEntry
			if err := db.Where("billing_order_id = ? AND type = ?", order.ID, model.CreditLedgerConsume).Find(&consumes).Error; err != nil {
				t.Fatal(err)
			}
			if len(consumes) != 1 || consumes[0].AmountMicrocredits != -tc.want {
				t.Fatalf("钱包一笔 consume 等于实扣：%+v", consumes)
			}
			if summary := taskBillingSummary(*order); summary == nil || summary.ActualAmountMicrocredits == nil || *summary.ActualAmountMicrocredits != tc.want {
				t.Fatalf("任务摘要显示的实扣与流水一致：%+v", summary)
			}
			var account model.CreditAccount
			if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
				t.Fatal(err)
			}
			if account.ReservedMicrocredits != 0 || account.AvailableMicrocredits != 10_000_000-(tc.want-tc.reserved) {
				t.Fatalf("预授权多退少补：%+v", account)
			}
		})
	}
}

// 逐轮续写建单时按这次要调的轮数放大预授权（单轮估算 × 轮数）；「从第 N 节继续」只算剩下的轮。
func TestDramaRunBillingReservesEveryTurn(t *testing.T) {
	svc, db := newDramaWorkerTestService(t)
	project, unit := seedLongQichuang(t, svc, db, 2, 4)
	for _, row := range []any{
		&model.ModelChannel{ID: "ch-text", Scope: "system", Enabled: true, Name: "text", BaseURL: "https://upstream.example.com/v1", APIFormat: "openai"},
		&model.ChannelModel{ID: "cm-text", ChannelID: "ch-text", ModelKey: "fake-shots", Capability: "text", Protocol: "chat-completion", CapabilityConfigJSON: mustEncodeModelCapabilityConfig(t, DefaultModelCapabilityConfigForModel("chat-completion", "fake-shots")), BillingMode: "fixed_request", UnitPriceMicrocredits: 300_000, PriceConfigured: true, Enabled: true},
		&model.ChannelModelPriceTier{ID: "tier-text", ChannelModelID: "cm-text", SelectorKey: "{}", SelectorJSON: "{}", BillingMode: "fixed_request", UnitPriceMicrocredits: 300_000, PriceConfigured: true, Enabled: true},
		&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 10_000_000},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	runReq := DramaStepRunRequest{Model: "fake-shots", Config: map[string]any{"channelId": "ch-text", "model": "fake-shots", "textModel": "fake-shots"}}
	created, err := svc.RunDramaStep("user-1", project.ID, unit.ID, drama.StepChapterShots, runReq)
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := svc.repo.Task(created.Task.ID)
	order, err := svc.repo.BillingOrder(stored.BillingOrderID)
	if err != nil {
		t.Fatal(err)
	}
	if order.Quantity != 4 || order.AmountMicrocredits != 4*300_000 || order.ReservedAmountMicrocredits != 4*300_000 {
		t.Fatalf("4 节按 4 次预授权：quantity %d amount %d reserved %d", order.Quantity, order.AmountMicrocredits, order.ReservedAmountMicrocredits)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.ReservedMicrocredits != 4*300_000 {
		t.Fatalf("冻结 4 次：%+v", account)
	}

	// 普通单轮文本任务（没有 dramaRun）预授权不变。
	single, err := svc.taskBillingOrder("user-1", &model.Task{ID: "single", Type: "canvas_text", Operation: "text"}, map[string]any{"mode": "text", "config": map[string]any{"channelId": "ch-text", "model": "fake-shots"}})
	if err != nil || single == nil || single.Quantity != 1 || single.AmountMicrocredits != 300_000 {
		t.Fatalf("单轮文本任务仍按一次预授权：%+v %v", single, err)
	}
}

func TestDramaRunUpstreamCalls(t *testing.T) {
	scenes := []drama.ShotsScene{{ID: "E01S01", Section: "E01S01-P1"}, {ID: "E01S01", Section: "E01S01-P2"}, {ID: "E01S02", Section: "E01S02-P1"}, {ID: "E01S02", Section: "E01S02-P2"}}
	for _, tc := range []struct {
		name string
		run  *dramaRunMeta
		want int64
	}{
		{"不是逐轮续写", nil, 1},
		{"一轮", &dramaRunMeta{Turns: 1, ResumeFrom: 1}, 1},
		{"4 节", &dramaRunMeta{Turns: 4, ResumeFrom: 1, Scenes: scenes}, 4},
		{"从第 3 节继续", &dramaRunMeta{Turns: 4, ResumeFrom: 3, Scenes: scenes}, 2},
		{"只重写 1 节", &dramaRunMeta{Turns: 4, ResumeFrom: 1, Scenes: scenes, OnlySections: []string{"E01S02-P1"}}, 1},
	} {
		if got := dramaRunUpstreamCalls(tc.run); got != tc.want {
			t.Errorf("%s：%d，应为 %d", tc.name, got, tc.want)
		}
	}
	order := &model.BillingOrder{Capability: "text", BillingMode: "token", Quantity: 5000, AmountMicrocredits: 7000}
	if err := scaleBillingOrderForCalls(order, 3); err != nil || order.AmountMicrocredits != 21_000 || order.ReservedAmountMicrocredits != 21_000 || order.Quantity != 15_000 {
		t.Fatalf("Token 预授权按 3 轮放大：%+v %v", order, err)
	}
	video := &model.BillingOrder{Capability: "video", BillingMode: "token", Quantity: 5000, AmountMicrocredits: 7000}
	if err := scaleBillingOrderForCalls(video, 3); err != nil || video.AmountMicrocredits != 7000 {
		t.Fatalf("视频订单不放大：%+v %v", video, err)
	}
}
