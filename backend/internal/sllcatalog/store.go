package sllcatalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"yingce/backend/internal/database"
	"yingce/backend/internal/kernel"
	"yingce/backend/internal/model"
)

const maxCostMicroCNY = 6_000_000

var seedance25 = regexp.MustCompile(`(?i)(^|[^a-z0-9])seedance[ ._-]*2[._-]5([^0-9]|$)`)
var errAmbiguousMapping = errors.New("后台已有冲突的上游映射")

type Store struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

func IsSLLChannel(ch model.ModelChannel) bool {
	u, err := url.Parse(strings.TrimSpace(ch.BaseURL))
	return err == nil && ch.Scope == model.ChannelScopeSystem && u.Scheme == "https" && u.User == nil &&
		strings.EqualFold(u.Hostname(), "api.lecapi.net") && (u.Port() == "" || u.Port() == "443")
}

// Changing the endpoint or credential invalidates the operator's group confirmation.
func credentialDigest(ch model.ModelChannel) string {
	b, _ := json.Marshal([]string{ch.BaseURL, ch.APIKey, ch.SecretKey, ch.HeadersJSON})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

type View struct {
	NeedsGroupConfirmation bool         `json:"needsGroupConfirmation"`
	Group                  string       `json:"group"`
	Source                 string       `json:"source"`
	Version                string       `json:"version"`
	CollectedAt            *time.Time   `json:"collectedAt"`
	Added                  int          `json:"added"`
	Updated                int          `json:"updated"`
	Excluded               []Exclusion  `json:"excluded"`
	Rows                   []CatalogRow `json:"rows"`
}

type CatalogRow struct {
	Description    string       `json:"description"`
	GroupRatio     float64      `json:"groupRatio"`
	Currency       string       `json:"currency"`
	BillingUnit    string       `json:"billingUnit"`
	ModelID        string       `json:"modelId"`
	DisplayName    string       `json:"displayName"`
	ChannelModelID string       `json:"channelModelId"`
	Group          string       `json:"group"`
	Enabled        bool         `json:"enabled"`
	Available      bool         `json:"available"`
	Reason         string       `json:"reason"`
	Capabilities   Capabilities `json:"capabilities"`
	Tiers          []Tier       `json:"tiers"`
	Metric         RateView     `json:"metric"`
}

func (s *Store) HasImportedChannels(ctx context.Context) (bool, error) {
	var count int64
	err := s.db.WithContext(ctx).Model(&model.SLLCatalogImport{}).
		Joins("JOIN model_channels ON model_channels.id = sll_catalog_imports.channel_id AND model_channels.deleted_at IS NULL").
		Count(&count).Error
	return count > 0, err
}

func (s *Store) Import(ctx context.Context, channelID, group string, catalog Catalog, at time.Time, expected ...model.ModelChannel) (View, error) {
	if strings.TrimSpace(group) == "" {
		return View{}, errors.New("请确认此渠道凭据实际使用的 SLL 分组")
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ch model.ModelChannel
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&ch, "id = ?", channelID).Error; err != nil {
			return err
		}
		if !IsSLLChannel(ch) {
			return errors.New("需要 SLL 官方系统渠道")
		}
		if len(expected) > 0 && credentialDigest(expected[0]) != credentialDigest(ch) {
			return kernel.NewAppError(409, "读取价目时渠道凭据发生变更，请确认分组后重新同步")
		}
		var previous model.SLLCatalogImport
		if err := tx.Where("channel_id = ?", channelID).Find(&previous).Error; err != nil {
			return err
		}
		if previous.CollectedAt.After(at) {
			return kernel.NewAppError(409, "已有更新的目录同步结果，请重新读取")
		}
		state := model.SLLCatalogImport{ChannelID: channelID, SupplierGroup: group, CredentialDigest: credentialDigest(ch), SourceVersion: catalog.Version, CollectedAt: at.UTC()}
		excluded := append([]Exclusion{}, catalog.Excluded...)
		active := make(map[string]bool)
		for _, e := range catalog.Entries {
			if e.ModelID == "" || e.Group != group || active[e.ModelID] {
				return errors.New("目录模型或实际分组不一致")
			}
			proto, config, err := ExecutionConfig(e)
			if err != nil {
				excluded = append(excluded, Exclusion{ModelID: e.ModelID, Reason: err.Error()})
				continue
			}
			item, exists, err := findChannelItem(tx, channelID, e.ModelID)
			if errors.Is(err, errAmbiguousMapping) {
				excluded = append(excluded, Exclusion{ModelID: e.ModelID, Reason: err.Error()})
				continue
			}
			if err != nil {
				return err
			}
			if exists && item.DeletedAt.Valid {
				excluded = append(excluded, Exclusion{ModelID: e.ModelID, Reason: "后台已删除模型"})
				continue
			}
			changed, err := saveCatalogItem(tx, channelID, e, proto, config, &item, exists)
			if err != nil {
				return err
			}
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			var old model.SLLCatalogLine
			if err := tx.Where("channel_id = ? AND model_id = ?", channelID, e.ModelID).Find(&old).Error; err != nil {
				return err
			}
			line := model.SLLCatalogLine{ChannelID: channelID, ModelID: e.ModelID, ChannelModelID: item.ID, EntryJSON: string(b), Available: true}
			if old != line {
				if err := tx.Save(&line).Error; err != nil {
					return err
				}
				changed = true
			}
			switch {
			case !exists:
				state.Added++
			case changed:
				state.Updated++
			}
			active[e.ModelID] = true
		}
		var lines []model.SLLCatalogLine
		if err := tx.Where("channel_id = ? AND available = ?", channelID, true).Find(&lines).Error; err != nil {
			return err
		}
		for _, line := range lines {
			if active[line.ModelID] {
				continue
			}
			reason := "供应商已下架或全部规格被排除"
			for _, x := range excluded {
				if x.ModelID == line.ModelID {
					reason = x.Reason
					break
				}
			}
			excluded = append(excluded, Exclusion{ModelID: line.ModelID, Reason: reason})
			if err := tx.Model(&line).Updates(map[string]any{"available": false, "reason": reason}).Error; err != nil {
				return err
			}
			if err := retireCatalogItem(tx, line.ChannelModelID); err != nil {
				return err
			}
		}
		// Existing Seedance lines that never passed this import must also leave the normal picker.
		var existing []model.ChannelModel
		if err := tx.Where("channel_id = ? AND capability = ?", channelID, "video").Find(&existing).Error; err != nil {
			return err
		}
		for _, item := range existing {
			upstream := item.ProviderModelKey
			if upstream == "" {
				upstream = item.ModelKey
			}
			if active[upstream] || !seedance25.MatchString(upstream) {
				continue
			}
			if err := retireCatalogItem(tx, item.ID); err != nil {
				return err
			}
		}
		b, err := json.Marshal(excluded)
		if err != nil {
			return err
		}
		state.ExclusionsJSON = string(b)
		return tx.Save(&state).Error
	})
	if err != nil {
		return View{}, err
	}
	return s.View(ctx, channelID, at)
}

func findChannelItem(tx *gorm.DB, channelID, key string) (model.ChannelModel, bool, error) {
	var items []model.ChannelModel
	err := tx.Unscoped().Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("channel_id = ? AND (model_key = ? OR provider_model_key = ?)", channelID, key, key).
		Order("CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, created_at DESC, id ASC").Find(&items).Error
	if err != nil || len(items) == 0 {
		return model.ChannelModel{}, false, err
	}
	if len(items) > 1 && !items[1].DeletedAt.Valid || !items[0].DeletedAt.Valid && items[0].ProviderModelKey != "" && items[0].ProviderModelKey != key {
		return model.ChannelModel{}, false, fmt.Errorf("%w，请核对模型 %s 的调用标识和上游 ID", errAmbiguousMapping, key)
	}
	return items[0], true, nil
}

func retireCatalogItem(tx *gorm.DB, id string) error {
	if err := tx.Where("channel_model_id = ?", id).Delete(&model.ChannelModelPriceTier{}).Error; err != nil {
		return err
	}
	return tx.Model(&model.ChannelModel{}).Where("id = ? AND price_configured = ?", id, true).
		Updates(map[string]any{"price_configured": false, "price_version": gorm.Expr("price_version + 1")}).Error
}

func saveCatalogItem(tx *gorm.DB, channelID string, e Entry, proto model.ChannelInterfaceType, config string, item *model.ChannelModel, exists bool) (bool, error) {
	if !exists {
		id, err := database.AllocatePrefixedID(tx, "MODEL")
		if err != nil {
			return false, err
		}
		*item = model.ChannelModel{ID: id, ChannelID: channelID, ModelKey: e.ModelID, Enabled: true, PriceCurrency: model.PriceCurrencyCNY}
	}
	before := *item
	item.ProviderModelKey, item.DisplayName, item.Capability, item.Protocol = e.ModelID, e.DisplayName, "video", proto
	item.CatalogModelKey = e.ModelID
	item.BillingMode = "fixed_request"
	if item.CapabilityConfigJSON != config {
		item.CapabilityConfigJSON = config
		item.CapabilityVersion++
	}
	// PostgreSQL varchar(500) is measured in characters.
	description := []rune(e.Description)
	item.Description = string(description[:min(500, len(description))])
	if !exists {
		if err := tx.Create(item).Error; err != nil {
			return false, err
		}
	}
	changed, err := syncTiers(tx, item, e, before)
	if err != nil {
		return false, err
	}
	if exists && !changed && reflect.DeepEqual(before, *item) {
		return false, nil
	}
	item.PriceVersion++
	return true, tx.Save(item).Error
}

func syncTiers(tx *gorm.DB, item *model.ChannelModel, e Entry, previous model.ChannelModel) (bool, error) {
	var existing []model.ChannelModelPriceTier
	if err := tx.Unscoped().Where("channel_model_id = ?", item.ID).
		Order("CASE WHEN deleted_at IS NULL THEN 0 ELSE 1 END, updated_at DESC, id ASC").Find(&existing).Error; err != nil {
		return false, err
	}
	byKey := make(map[string]model.ChannelModelPriceTier)
	for _, old := range existing {
		if _, ok := byKey[old.SelectorKey]; !ok {
			byKey[old.SelectorKey] = old
		}
	}
	keep := make(map[string]bool)
	item.PriceConfigured = false
	item.UnitPriceMicrocredits = 0
	changed := false
	for _, spec := range e.Tiers {
		if spec.CostMicroCNY < 0 || spec.CostMicroCNY > maxCostMicroCNY || spec.Resolution == "" || spec.Resolution == "*" {
			return false, errors.New("目录中存在未核实的人民币按条规格")
		}
		_, key, err := model.CanonicalSKUSelector(map[string]string{"vquality": spec.Resolution})
		if err != nil {
			return false, err
		}
		tier, found := byKey[key]
		if !found {
			id, err := database.AllocatePrefixedID(tx, "PTIER")
			if err != nil {
				return false, err
			}
			tier = model.ChannelModelPriceTier{ID: id, ChannelModelID: item.ID, Enabled: true, SelectorJSON: key, SelectorKey: key, Resolution: spec.Resolution, BillingMode: "fixed_request"}
			if base, ok := byKey["{}"]; ok && !base.DeletedAt.Valid && base.BillingMode == "fixed_request" && (base.ProviderModelKey == "" || base.ProviderModelKey == e.ModelID) {
				tier.UnitPriceMicrocredits, tier.PriceConfigured, tier.CostPricing, tier.Enabled = base.UnitPriceMicrocredits, base.PriceConfigured, base.CostPricing, base.Enabled
			} else if len(existing) == 0 && previous.BillingMode == "fixed_request" && previous.PriceConfigured {
				tier.UnitPriceMicrocredits, tier.PriceConfigured = previous.UnitPriceMicrocredits, true
			} else if item.PriceCurrency == model.PriceCurrencyCNY {
				tier.UnitPriceMicrocredits, tier.PriceConfigured = spec.CostMicroCNY, true
			}
		}
		before := tier
		// A price from a different billing unit cannot be carried over as a per-request sale.
		if tier.BillingMode != "fixed_request" {
			tier.PriceConfigured = false
			tier.UnitPriceMicrocredits = 0
			tier.CostPricing = model.CreditCostPricing{}
		}
		tier.BillingMode, tier.ProviderModelKey, tier.UpstreamRequestCostMicroCNY = "fixed_request", e.ModelID, &spec.CostMicroCNY
		tier.DeletedAt = gorm.DeletedAt{}
		if !found || !reflect.DeepEqual(before, tier) {
			tier.PriceVersion++
			if err := tx.Unscoped().Save(&tier).Error; err != nil {
				return false, err
			}
			changed = true
		}
		keep[tier.ID] = true
		if tier.Enabled && tier.PriceConfigured {
			if !item.PriceConfigured || tier.UnitPriceMicrocredits < item.UnitPriceMicrocredits {
				item.UnitPriceMicrocredits = tier.UnitPriceMicrocredits
			}
			item.PriceConfigured = true
		}
	}
	for _, old := range existing {
		if old.DeletedAt.Valid || keep[old.ID] {
			continue
		}
		if err := tx.Delete(&old).Error; err != nil {
			return false, err
		}
		changed = true
	}
	return changed, nil
}
