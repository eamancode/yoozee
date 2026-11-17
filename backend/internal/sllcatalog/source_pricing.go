package sllcatalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"slices"
	"strconv"
	"strings"
)

type priceTier struct {
	ModelID    string      `json:"model_name"`
	Resolution string      `json:"resolution"`
	Type       string      `json:"tier_type"`
	Unit       string      `json:"billing_unit"`
	Currency   string      `json:"currency"`
	Price      json.Number `json:"model_price"`
	Seconds    int         `json:"duration_seconds"`
}

type priceModel struct {
	ModelID     string      `json:"model_name"`
	DisplayName string      `json:"display_name"`
	Description string      `json:"description"`
	Currency    string      `json:"currency"`
	QuotaType   *int        `json:"quota_type"`
	Endpoints   []string    `json:"supported_endpoint_types"`
	Groups      []string    `json:"enable_groups"`
	BillingExpr string      `json:"billing_expr"`
	BillingMode string      `json:"billing_mode"`
	Tiers       []priceTier `json:"price_tiers"`
}

type priceList struct {
	Success bool                   `json:"success"`
	Data    []priceModel           `json:"data"`
	Ratios  map[string]json.Number `json:"group_ratio"`
	Version string                 `json:"pricing_version"`
}

type mediaList struct {
	Success bool         `json:"success"`
	Data    []mediaModel `json:"data"`
	Version string       `json:"catalog_version"`
}

type publicStatus struct {
	Success bool `json:"success"`
	Data    struct {
		Currency string      `json:"quota_display_type"`
		Exchange json.Number `json:"usd_exchange_rate"`
	} `json:"data"`
}

// ParsePricing joins the supplier's independently published documents by exact
// upstream ID. The endpoint-level schema determines which quoted tiers the
// adapter can actually select. A model-level "from" price never authorizes a tier.
func ParsePricing(pricing, media, status io.Reader, group string) (Catalog, error) {
	var list priceList
	var registry mediaList
	var currency publicStatus
	for _, doc := range []struct {
		r io.Reader
		v any
	}{{pricing, &list}, {media, &registry}, {status, &currency}} {
		if err := decodePublic(doc.r, doc.v); err != nil {
			return Catalog{}, err
		}
	}
	if !list.Success || !registry.Success || !currency.Success || list.Data == nil || registry.Data == nil || list.Ratios == nil {
		return Catalog{}, errors.New("SLL 公开目录结构不完整")
	}
	models := make(map[string]mediaModel, len(registry.Data))
	for _, m := range registry.Data {
		if _, exists := models[m.ID]; exists || m.ID == "" {
			return Catalog{}, errors.New("SLL 能力目录模型 ID 缺失或重复")
		}
		models[m.ID] = m
	}
	seen := make(map[string]bool)
	result := Catalog{Version: list.Version + "/" + registry.Version, Entries: []Entry{}, Excluded: []Exclusion{}}
	// The public request-tier numbers are verified as CNY with a 1:1 display
	// rate. An unverified conversion must not be silently treated as yuan.
	exchange := decimal(currency.Data.Exchange)
	isCNY := currency.Data.Currency == "CNY" && exchange != nil && exchange.Cmp(big.NewRat(1, 1)) == 0
	groupRatio := decimal(list.Ratios[group])
	for _, p := range list.Data {
		if seen[p.ModelID] || p.ModelID == "" {
			return Catalog{}, errors.New("SLL 价目模型 ID 缺失或重复")
		}
		seen[p.ModelID] = true
		entry, exclusions := evaluateEntry(p, models[p.ModelID], group, groupRatio, isCNY)
		result.Excluded = append(result.Excluded, exclusions...)
		if len(entry.Tiers) > 0 {
			result.Entries = append(result.Entries, entry)
		}
	}
	return result, nil
}

func evaluateEntry(p priceModel, m mediaModel, group string, ratio *big.Rat, isCNY bool) (Entry, []Exclusion) {
	drop := func(reason string) (Entry, []Exclusion) {
		return Entry{}, []Exclusion{{ModelID: p.ModelID, Reason: reason}}
	}
	if !slices.Contains(p.Endpoints, "openai-video") || m.Identity.FamilyID != "seedance-2.5" {
		return drop("不是有公开规格的 Seedance 2.5 视频线路")
	}
	if !isCNY || !knownCurrency(p.Currency) {
		return drop("人民币计价或换算依据未核实")
	}
	if group == "" || ratio == nil || ratio.Sign() <= 0 || !slices.Contains(p.Groups, group) {
		return drop("实际调用分组未开放或倍率未核实")
	}
	if p.QuotaType == nil || *p.QuotaType != 1 || p.BillingExpr != "" || p.BillingMode == "tiered_expr" {
		return drop("未核实人民币固定按条计价")
	}
	caps, resolutions, err := m.capabilities()
	if err != nil {
		return drop(err.Error())
	}
	if len(p.Tiers) == 0 || p.DisplayName == "" {
		return drop("缺少公开展示名或实际请求价档")
	}
	ratioFloat, _ := ratio.Float64()
	entry := Entry{ModelID: p.ModelID, DisplayName: p.DisplayName, Description: p.Description, Group: group, GroupRatio: ratioFloat, Capabilities: caps}
	var excluded []Exclusion
	resCounts := make(map[string]int)
	for _, t := range p.Tiers {
		if t.Resolution == "" {
			return drop("存在未标明分辨率的价档，无法限制实际请求")
		}
		resCounts[t.Resolution]++
	}
	for _, t := range p.Tiers {
		tier, reason := eligibleTier(p.ModelID, t, resCounts[t.Resolution], resolutions, ratio)
		if reason != "" {
			excluded = append(excluded, Exclusion{p.ModelID, t.Resolution, reason})
		} else {
			entry.Tiers = append(entry.Tiers, tier)
		}
	}
	return entry, excluded
}

func eligibleTier(id string, t priceTier, count int, resolutions []string, ratio *big.Rat) (Tier, string) {
	switch {
	case t.ModelID != id || !slices.Contains(resolutions, t.Resolution):
		return Tier{}, "模型或分辨率与可提交规格不符"
	case count != 1 || t.Seconds != 0 || t.Type != "resolution" && t.Type != "request":
		return Tier{}, "存在无法按单一分辨率约束的条件价档"
	case t.Unit != "request":
		return Tier{}, "不按条或按次计价"
	case !knownCurrency(t.Currency):
		return Tier{}, "价档币种未核实为人民币"
	}
	price := decimal(t.Price)
	if price == nil || price.Sign() < 0 {
		return Tier{}, "缺少有效的公开单价"
	}
	price.Mul(price, ratio)
	if price.Cmp(big.NewRat(6, 1)) > 0 {
		return Tier{}, "实际分组单条成本超过人民币 6 元"
	}
	priceFloat, _ := price.Float64()
	price.Mul(price, big.NewRat(1_000_000, 1))
	whole, remainder := new(big.Int), new(big.Int)
	whole.QuoRem(price.Num(), price.Denom(), remainder)
	micro := whole.Int64()
	if remainder.Sign() != 0 {
		micro++
	}
	return Tier{t.Resolution, priceFloat, micro}, ""
}

func knownCurrency(value string) bool {
	return value == "" || strings.EqualFold(value, "CNY") || strings.EqualFold(value, "RMB")
}

// Limit exponents and literal length before arbitrary-precision conversion.
func decimal(n json.Number) *big.Rat {
	s := n.String()
	if len(s) == 0 || len(s) > 40 {
		return nil
	}
	if _, err := strconv.ParseFloat(s, 64); err != nil {
		return nil
	}
	if i := strings.IndexAny(s, "eE"); i >= 0 {
		exponent, err := strconv.Atoi(s[i+1:])
		if err != nil || exponent < -30 || exponent > 30 {
			return nil
		}
	}
	value, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil
	}
	return value
}

const maxPublicDocument = 16 << 20

func decodePublic(r io.Reader, dest any) error {
	if r == nil {
		return errors.New("SLL 公开响应为空")
	}
	b, err := io.ReadAll(io.LimitReader(r, maxPublicDocument+1))
	if err != nil {
		return fmt.Errorf("读取 SLL 公开响应: %w", err)
	}
	if len(b) > maxPublicDocument {
		return errors.New("SLL 公开响应过大")
	}
	if err := json.Unmarshal(b, dest); err != nil {
		return fmt.Errorf("解析 SLL 公开 JSON: %w", err)
	}
	return nil
}
