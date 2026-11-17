package sllcatalog

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/model"
)

type RateView struct {
	State       string     `json:"state"`
	Source      string     `json:"source"`
	WindowHours int        `json:"windowHours"`
	Rate        *float64   `json:"rate"`
	SampleCount *int64     `json:"sampleCount"`
	CollectedAt *time.Time `json:"collectedAt"`
	AttemptedAt *time.Time `json:"attemptedAt"`
	Stale       bool       `json:"stale"`
}

func (s *Store) RecordMetrics(ctx context.Context, at time.Time, metrics map[string]Metric, fetchErr error) error {
	if at.IsZero() {
		return errors.New("采集时间为空")
	}
	row := model.SLLMetricHour{Hour: at.UTC().Truncate(time.Hour), AttemptedAt: at.UTC(), Successful: fetchErr == nil}
	if row.Successful {
		if metrics == nil {
			return errors.New("成功率响应为空")
		}
		for _, m := range metrics {
			if m.Rate != nil && (math.IsNaN(*m.Rate) || *m.Rate < 0 || *m.Rate > 100) || m.SampleCount != nil && *m.SampleCount < 0 {
				return errors.New("成功率或样本数无效")
			}
		}
		data, err := json.Marshal(metrics)
		if err != nil {
			return err
		}
		row.MetricsJSON = string(data)
	}
	// A successful snapshot wins the hour. A retry may replace a failed attempt.
	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "hour"}},
		DoUpdates: clause.AssignmentColumns([]string{"attempted_at", "successful", "metrics_json"}),
		Where:     clause.Where{Exprs: []clause.Expression{clause.Expr{SQL: "sll_metric_hours.successful = ? AND (excluded.successful = ? OR excluded.attempted_at > sll_metric_hours.attempted_at)", Vars: []any{false, true}}}},
	}).Create(&row).Error
}

type metricSnapshot struct {
	latest, good model.SLLMetricHour
	values       map[string]Metric
}

func readSnapshot(db *gorm.DB, now time.Time) (metricSnapshot, error) {
	var snap metricSnapshot
	q := func() *gorm.DB {
		return db.Model(&model.SLLMetricHour{}).Where("attempted_at <= ?", now.UTC()).Order("hour DESC").Limit(1)
	}
	if err := q().Find(&snap.latest).Error; err != nil {
		return snap, err
	}
	if snap.latest.Successful {
		snap.good = snap.latest
	} else if err := q().Where("successful = ?", true).Find(&snap.good).Error; err != nil {
		return snap, err
	}
	if snap.good.Successful {
		if err := json.Unmarshal([]byte(snap.good.MetricsJSON), &snap.values); err != nil {
			return snap, err
		}
	}
	return snap, nil
}

func (s metricSnapshot) view(id string, now time.Time) RateView {
	out := RateView{State: "never", Source: MetricsURL, WindowHours: 24}
	if s.latest.Hour.IsZero() {
		return out
	}
	out.AttemptedAt = &s.latest.AttemptedAt
	if s.good.Successful {
		out.CollectedAt = &s.good.AttemptedAt
		out.Stale = !now.Before(s.good.AttemptedAt.Add(2 * time.Hour))
		out.State = "unavailable"
		if m, ok := s.values[id]; ok {
			out.Rate, out.SampleCount = m.Rate, m.SampleCount
			if m.Rate != nil {
				out.State = "value"
			}
		}
	}
	if !s.latest.Successful {
		out.State = "fetch_failed"
	}
	return out
}

func (s *Store) View(ctx context.Context, channelID string, now time.Time) (View, error) {
	out := View{Source: PricingURL, Rows: []CatalogRow{}, Excluded: []Exclusion{}}
	db := s.db.WithContext(ctx)
	var ch model.ModelChannel
	if err := db.First(&ch, "id = ?", channelID).Error; err != nil {
		return out, err
	}
	if !IsSLLChannel(ch) {
		return out, errors.New("需要 SLL 官方系统渠道")
	}
	var cfg model.SLLCatalogImport
	if err := db.Where("channel_id = ?", channelID).Find(&cfg).Error; err != nil {
		return out, err
	}
	if cfg.ChannelID == "" {
		return out, nil
	}
	out.Group, out.Version, out.CollectedAt, out.Added, out.Updated = cfg.SupplierGroup, cfg.SourceVersion, &cfg.CollectedAt, cfg.Added, cfg.Updated
	out.NeedsGroupConfirmation = cfg.CredentialDigest != credentialDigest(ch)
	if err := json.Unmarshal([]byte(cfg.ExclusionsJSON), &out.Excluded); err != nil {
		return out, err
	}
	var lines []model.SLLCatalogLine
	if err := db.Where("channel_id = ?", channelID).Order("model_id ASC").Find(&lines).Error; err != nil {
		return out, err
	}
	var items []model.ChannelModel
	if err := db.Select("id", "enabled", "price_configured").Where("channel_id = ?", channelID).Find(&items).Error; err != nil {
		return out, err
	}
	byID := make(map[string]model.ChannelModel, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	snap, err := readSnapshot(db, now)
	if err != nil {
		return out, err
	}
	for _, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line.EntryJSON), &e); err != nil {
			return out, err
		}
		item, present := byID[line.ChannelModelID]
		row := CatalogRow{ModelID: line.ModelID, DisplayName: e.DisplayName, ChannelModelID: item.ID, Enabled: item.Enabled, Available: line.Available, Reason: line.Reason, Group: e.Group, Capabilities: e.Capabilities, Tiers: e.Tiers, Metric: snap.view(e.ModelID, now)}
		row.GroupRatio, row.Currency, row.BillingUnit = e.GroupRatio, "CNY", "request"
		row.Description = e.Description
		switch {
		case !present:
			row.Available = false
			row.Reason = "后台已删除模型"
		case cfg.CredentialDigest != credentialDigest(ch):
			row.Available = false
			row.Reason = "渠道凭据已变更，请重新确认上游分组并同步"
		case line.Available && !ch.Enabled:
			row.Enabled = false
			row.Reason = "系统渠道已停用"
		case line.Available && !item.PriceConfigured:
			row.Available = false
			row.Reason = "请配置并启用平台售价"
		}
		out.Rows = append(out.Rows, row)
	}
	return out, nil
}
