package sllcatalog

import (
	"context"
	"encoding/json"
	"time"

	"gorm.io/gorm"
	"yingce/backend/internal/model"
)

type Offer struct {
	ChannelID, ChannelModelID, ModelKey, ProviderModelKey, PriceTierID    string
	DisplayName, Supplier, Resolution, SelectorJSON, CapabilityConfigJSON string
	CostMicroCNY                                                          int64
	Official, Platform                                                    RateView
}

type routeIdentity struct{ channel, upstream string }

const platformSource = "yingce:terminal-video-tasks"

// Offers enumerates exact eligible SKUs. The caller projects the storyboard's actual
// input against these SKUs and rechecks the chosen SKU at quote / confirmation.
func (s *Store) Offers(ctx context.Context, allowedChannelIDs []string, now time.Time) ([]Offer, error) {
	out := []Offer{}
	if len(allowedChannelIDs) == 0 {
		return out, nil
	}
	db := s.db.WithContext(ctx)
	var channels []model.ModelChannel
	if err := db.Where("id IN ? AND scope = ? AND enabled = ?", allowedChannelIDs, model.ChannelScopeSystem, true).Find(&channels).Error; err != nil {
		return nil, err
	}
	active := map[string]model.ModelChannel{}
	for _, ch := range channels {
		active[ch.ID] = ch
	}
	var items []model.ChannelModel
	if err := db.Where("channel_id IN ? AND capability = ? AND enabled = ? AND price_configured = ?", allowedChannelIDs, "video", true, true).Find(&items).Error; err != nil {
		return nil, err
	}
	byID := map[string]model.ChannelModel{}
	ids := []string{}
	for _, item := range items {
		if _, ok := active[item.ChannelID]; ok {
			byID[item.ID] = item
			ids = append(ids, item.ID)
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var tiers []model.ChannelModelPriceTier
	if err := db.Where("channel_model_id IN ? AND billing_mode = ? AND enabled = ? AND price_configured = ?", ids, "fixed_request", true, true).Order("channel_model_id ASC, id ASC").Find(&tiers).Error; err != nil {
		return nil, err
	}
	var states []model.SLLCatalogImport
	if err := db.Where("channel_id IN ?", allowedChannelIDs).Find(&states).Error; err != nil {
		return nil, err
	}
	cfg := map[string]model.SLLCatalogImport{}
	for _, state := range states {
		cfg[state.ChannelID] = state
	}
	var lines []model.SLLCatalogLine
	if err := db.Where("channel_model_id IN ? AND available = ?", ids, true).Find(&lines).Error; err != nil {
		return nil, err
	}
	entries := map[string]Entry{}
	for _, line := range lines {
		var e Entry
		if err := json.Unmarshal([]byte(line.EntryJSON), &e); err != nil {
			return nil, err
		}
		entries[line.ChannelModelID] = e
	}
	snap, err := readSnapshot(db, now)
	if err != nil {
		return nil, err
	}
	rates, err := platformRates(db, allowedChannelIDs, now)
	if err != nil {
		return nil, err
	}
	for _, t := range tiers {
		item := byID[t.ChannelModelID]
		ch := active[item.ChannelID]
		upstream := t.ProviderModelKey
		if upstream == "" {
			upstream = item.ProviderModelKey
		}
		if upstream == "" {
			continue
		}
		offer := Offer{ChannelID: ch.ID, ChannelModelID: item.ID, ModelKey: item.ModelKey, ProviderModelKey: upstream, PriceTierID: t.ID,
			DisplayName: item.DisplayName, Supplier: ch.ID, Resolution: t.Resolution, SelectorJSON: t.SelectorJSON, CapabilityConfigJSON: item.CapabilityConfigJSON,
			Official: RateView{State: "unavailable", WindowHours: 24}, Platform: RateView{State: "unavailable", Source: platformSource, WindowHours: 24, CollectedAt: &now}}
		if metric, ok := rates[routeIdentity{ch.ID, upstream}]; ok {
			offer.Platform = metric
		}
		if state, managed := cfg[ch.ID]; managed || IsSLLChannel(ch) {
			e, ok := entries[item.ID]
			if !managed || !ok || !IsSLLChannel(ch) || e.ModelID != upstream || item.ProviderModelKey != upstream || e.Group != state.SupplierGroup || credentialDigest(ch) != state.CredentialDigest || checkExecutionConfig(item, e) != nil {
				continue
			}
			_, key, err := model.CanonicalSKUSelector(map[string]string{"vquality": t.Resolution})
			if err != nil || key != t.SelectorKey {
				continue
			}
			var cost *int64
			for _, v := range e.Tiers {
				if v.Resolution == t.Resolution {
					cost = &v.CostMicroCNY
					break
				}
			}
			if cost == nil || *cost < 0 || *cost > maxCostMicroCNY {
				continue
			}
			offer.Supplier = "sll"
			offer.CostMicroCNY = *cost
			offer.Official = snap.view(upstream, now)
		} else {
			if !seedance25.MatchString(upstream) && !(item.ProviderModelKey == upstream && seedance25.MatchString(item.DisplayName)) {
				continue
			}
			if t.UpstreamRequestCostMicroCNY == nil || *t.UpstreamRequestCostMicroCNY < 0 || *t.UpstreamRequestCostMicroCNY > maxCostMicroCNY {
				continue
			}
			offer.CostMicroCNY = *t.UpstreamRequestCostMicroCNY
		}
		out = append(out, offer)
	}
	return out, nil
}

// Count each terminal task once. Polls/callbacks and local preflight errors have
// no provider POST. Ambiguous multi-provider tasks are excluded from both routes.
func platformRates(db *gorm.DB, channels []string, now time.Time) (map[routeIdentity]RateView, error) {
	type stat struct {
		ChannelID, ProviderKey string
		Total, Successes       int64
	}
	var stats []stat
	err := db.Raw(`
		SELECT channel_id, provider_key, COUNT(*) AS total,
		       SUM(CASE WHEN status = 'succeeded' THEN 1 ELSE 0 END) AS successes
		FROM (
			SELECT t.id, t.status, MIN(l.channel_id) AS channel_id, MIN(l.video_provider_model_key) AS provider_key
			FROM tasks t JOIN api_call_logs l ON l.task_id = t.id
			WHERE t.completed_at > ? AND t.completed_at <= ? AND t.status IN ('succeeded','failed')
			  AND l.capability = 'video' AND l.request_kind = 'create' AND l.method = 'POST' AND l.source = 'backend-task'
			GROUP BY t.id, t.status
			HAVING COUNT(DISTINCT l.channel_id) = 1 AND COUNT(DISTINCT l.video_provider_model_key) = 1
			       AND COUNT(l.video_provider_model_key) = COUNT(*) AND MIN(l.video_provider_model_key) <> ''
		) terminal
		WHERE channel_id IN ?
		GROUP BY channel_id, provider_key`, now.Add(-24*time.Hour).UTC(), now.UTC(), channels).Scan(&stats).Error
	if err != nil {
		return nil, err
	}
	out := make(map[routeIdentity]RateView, len(stats))
	for _, v := range stats {
		r := float64(v.Successes) * 100 / float64(v.Total)
		out[routeIdentity{v.ChannelID, v.ProviderKey}] = RateView{State: "value", Source: platformSource, WindowHours: 24, Rate: &r, SampleCount: &v.Total, CollectedAt: &now, AttemptedAt: &now}
	}
	return out, nil
}
