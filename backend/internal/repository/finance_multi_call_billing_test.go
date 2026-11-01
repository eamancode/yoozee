package repository

import (
	"fmt"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"yingce/backend/internal/model"
)

func newMultiCallBillingDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+name+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.CreditAccount{}, &model.BillingOrder{}, &model.ApiCallLog{}, &model.CreditLedgerEntry{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// opusTextOrder 是线上 opus 的文本 Token 单价：输入 5、缓存 2、输出 10 µ/token。
func opusTextOrder(reserved int64, status model.BillingStatus) model.BillingOrder {
	return model.BillingOrder{
		ID: "order-1", UserID: "user-1", IdempotencyKey: "task:task-1", Capability: "text", BillingMode: "token",
		AmountMicrocredits: reserved, ReservedAmountMicrocredits: reserved, MultiplierBasisPoints: 10_000,
		InputTokenPriceMicrocredits: 5_000_000, CachedTokenPriceMicrocredits: 2_000_000, OutputTokenPriceMicrocredits: 10_000_000,
		Status: status,
	}
}

func createCallLogs(t *testing.T, db *gorm.DB, logs []model.ApiCallLog) {
	t.Helper()
	base := time.Now().Add(-time.Hour)
	for index := range logs {
		logs[index].ID = fmt.Sprintf("log-%d", index+1)
		logs[index].BillingOrderID = "order-1"
		logs[index].Capability = "text"
		logs[index].CreatedAt = base.Add(time.Duration(index) * time.Minute)
		if err := db.Create(&logs[index]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

// 多轮文本订单按全部成功调用逐次计价相加：失败的调用（没有用量）和非计费的缓存创建不算，同一条日志只算一次。
func TestSettleTextTokenOrderSumsEveryCall(t *testing.T) {
	db := newMultiCallBillingDB(t, "finance-multi-call-token")
	if err := db.Create(&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 100_000, ReservedMicrocredits: 10_000}).Error; err != nil {
		t.Fatal(err)
	}
	order := opusTextOrder(10_000, model.BillingStatusRunning)
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	createCallLogs(t, db, []model.ApiCallLog{
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 1000, OutputTokens: 200},
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusFailed, StatusCode: 502},
		{RequestKind: "cache-create", Billable: false, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 9000},
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 1500, CachedTokens: 800, OutputTokens: 300},
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 2100, CachedTokens: 1500, OutputTokens: 250},
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 2700, CachedTokens: 2100, OutputTokens: 400},
	})

	repo := &Repository{db: db}
	if err := repo.SettleBillingOrder("order-1", ""); err != nil {
		t.Fatalf("SettleBillingOrder() error = %v", err)
	}
	// 再结算一次是幂等的，不会重复扣。
	if err := repo.SettleBillingOrder("order-1", ""); err != nil {
		t.Fatalf("second SettleBillingOrder() error = %v", err)
	}
	const want = int64(7000 + 8100 + 8500 + 11200)
	var settled model.BillingOrder
	if err := db.First(&settled, "id = ?", "order-1").Error; err != nil {
		t.Fatal(err)
	}
	if settled.Status != model.BillingStatusSettled || settled.ActualAmountMicrocredits != want || settled.RefundedAmountMicrocredits != 0 {
		t.Fatalf("settled order = %+v", settled)
	}
	if settled.InputTokens != 7300 || settled.CachedTokens != 4400 || settled.OutputTokens != 1150 || !settled.UsageAvailable {
		t.Fatalf("order usage = %d / %d / %d", settled.InputTokens, settled.CachedTokens, settled.OutputTokens)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.ReservedMicrocredits != 0 || account.AvailableMicrocredits != 100_000-(want-10_000) {
		t.Fatalf("account = %+v", account)
	}
	var entries []model.CreditLedgerEntry
	if err := db.Where("billing_order_id = ?", "order-1").Find(&entries).Error; err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Type != model.CreditLedgerConsume || entries[0].AmountMicrocredits != -want || entries[0].AvailableDeltaMicrocredits != -(want-10_000) {
		t.Fatalf("ledger = %+v", entries)
	}
	if entries[0].Note != "按 4 次上游调用的用量合计结算；Token 实际用量超过预授权，已补扣差额" {
		t.Fatalf("consume note = %q", entries[0].Note)
	}
}

// 单次调用的文本订单金额、流水与原来完全一致：按这一次的用量结算，预授权多出的退回，备注不变。
func TestSettleTextTokenOrderSingleCallUnchanged(t *testing.T) {
	db := newMultiCallBillingDB(t, "finance-multi-call-single")
	if err := db.Create(&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 100_000, ReservedMicrocredits: 90_000}).Error; err != nil {
		t.Fatal(err)
	}
	order := opusTextOrder(90_000, model.BillingStatusRunning)
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	// 线上风格分析 7f5a10bd：一次调用 7758 输入 / 3088 输出 → 69,670 µ。
	createCallLogs(t, db, []model.ApiCallLog{
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 7758, OutputTokens: 3088},
	})
	repo := &Repository{db: db}
	if err := repo.SettleBillingOrder("order-1", ""); err != nil {
		t.Fatalf("SettleBillingOrder() error = %v", err)
	}
	var settled model.BillingOrder
	if err := db.First(&settled, "id = ?", "order-1").Error; err != nil {
		t.Fatal(err)
	}
	if settled.ActualAmountMicrocredits != 69_670 || settled.RefundedAmountMicrocredits != 20_330 || settled.InputTokens != 7758 || settled.OutputTokens != 3088 {
		t.Fatalf("settled order = %+v", settled)
	}
	var consume, refund model.CreditLedgerEntry
	if err := db.First(&consume, "billing_order_id = ? AND type = ?", "order-1", model.CreditLedgerConsume).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&refund, "billing_order_id = ? AND type = ?", "order-1", model.CreditLedgerRefund).Error; err != nil {
		t.Fatal(err)
	}
	if consume.AmountMicrocredits != -69_670 || consume.Note != "" || refund.AmountMicrocredits != 20_330 || refund.Note != "Token 预授权差额退回" {
		t.Fatalf("ledger = %+v / %+v", consume, refund)
	}
}

// 按次计费的文本订单：成功调用 4 次结算 4 次（失败的那次不算）；只有 1 次时仍按建单金额，和原来一样。
func TestSettleTextFixedRequestOrderChargesEveryCall(t *testing.T) {
	for _, tc := range []struct {
		name     string
		quantity int64
		amount   int64
		calls    int
		want     int64
	}{
		{"按 4 轮预授权、4 次", 4, 1_200_000, 4, 1_200_000},
		{"按 1 次预授权、4 次补扣", 1, 300_000, 4, 1_200_000},
		{"单次", 1, 300_000, 1, 300_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newMultiCallBillingDB(t, "finance-multi-call-fixed-"+fmt.Sprint(tc.quantity)+"-"+fmt.Sprint(tc.calls))
			if err := db.Create(&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 5_000_000, ReservedMicrocredits: tc.amount}).Error; err != nil {
				t.Fatal(err)
			}
			order := model.BillingOrder{ID: "order-1", UserID: "user-1", IdempotencyKey: "task:task-1", Capability: "text", BillingMode: "fixed_request",
				UnitPriceMicrocredits: 300_000, MultiplierBasisPoints: 10_000, Quantity: tc.quantity, AmountMicrocredits: tc.amount, ReservedAmountMicrocredits: tc.amount,
				Status: model.BillingStatusRunning}
			if err := db.Create(&order).Error; err != nil {
				t.Fatal(err)
			}
			logs := []model.ApiCallLog{{RequestKind: "create", Billable: true, Status: model.ApiCallStatusFailed, StatusCode: 502}}
			for i := 0; i < tc.calls; i++ {
				logs = append(logs, model.ApiCallLog{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded})
			}
			createCallLogs(t, db, logs)
			repo := &Repository{db: db}
			if err := repo.SettleBillingOrder("order-1", ""); err != nil {
				t.Fatalf("SettleBillingOrder() error = %v", err)
			}
			var settled model.BillingOrder
			if err := db.First(&settled, "id = ?", "order-1").Error; err != nil {
				t.Fatal(err)
			}
			if settled.Status != model.BillingStatusSettled || settled.ActualAmountMicrocredits != tc.want {
				t.Fatalf("settled order = %+v", settled)
			}
			var account model.CreditAccount
			if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
				t.Fatal(err)
			}
			if account.ReservedMicrocredits != 0 || account.AvailableMicrocredits != 5_000_000-(tc.want-tc.amount) {
				t.Fatalf("account = %+v", account)
			}
			var consume model.CreditLedgerEntry
			if err := db.First(&consume, "billing_order_id = ? AND type = ?", "order-1", model.CreditLedgerConsume).Error; err != nil {
				t.Fatal(err)
			}
			if consume.AmountMicrocredits != -tc.want {
				t.Fatalf("consume = %+v", consume)
			}
		})
	}
}

// 退款后人工确认上游成功、重新扣费的多轮订单，同样按全部调用累加。
func TestRestoreRefundedTextTokenOrderSumsEveryCall(t *testing.T) {
	db := newMultiCallBillingDB(t, "finance-multi-call-restore")
	if err := db.Create(&model.CreditAccount{UserID: "user-1", AvailableMicrocredits: 100_000}).Error; err != nil {
		t.Fatal(err)
	}
	order := opusTextOrder(10_000, model.BillingStatusRefunded)
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	createCallLogs(t, db, []model.ApiCallLog{
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 1000, OutputTokens: 200},
		{RequestKind: "create", Billable: true, Status: model.ApiCallStatusSucceeded, UsageAvailable: true, InputTokens: 1500, CachedTokens: 800, OutputTokens: 300},
	})
	repo := &Repository{db: db}
	if err := repo.RestoreRefundedBillingOrder("order-1", ""); err != nil {
		t.Fatalf("RestoreRefundedBillingOrder() error = %v", err)
	}
	var settled model.BillingOrder
	if err := db.First(&settled, "id = ?", "order-1").Error; err != nil {
		t.Fatal(err)
	}
	if settled.Status != model.BillingStatusSettled || settled.ActualAmountMicrocredits != 7000+8100 || settled.InputTokens != 2500 {
		t.Fatalf("restored order = %+v", settled)
	}
	var account model.CreditAccount
	if err := db.First(&account, "user_id = ?", "user-1").Error; err != nil {
		t.Fatal(err)
	}
	if account.AvailableMicrocredits != 100_000-15_100 {
		t.Fatalf("account = %+v", account)
	}
}
