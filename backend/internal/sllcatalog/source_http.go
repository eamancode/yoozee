package sllcatalog

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"yingce/backend/internal/outbound"
)

func FetchPricing(ctx context.Context, client *http.Client, group string) (Catalog, error) {
	readers := make([]io.Reader, 0, 3)
	for _, url := range []string{PricingURL, MediaURL, StatusURL} {
		body, err := fetchPublic(ctx, client, url)
		if err != nil {
			return Catalog{}, err
		}
		readers = append(readers, bytes.NewReader(body))
	}
	return ParsePricing(readers[0], readers[1], readers[2], group)
}

func FetchMetrics(ctx context.Context, client *http.Client) (map[string]Metric, error) {
	body, err := fetchPublic(ctx, client, MetricsURL)
	if err != nil {
		return nil, err
	}
	return ParseMetrics(bytes.NewReader(body))
}

func fetchPublic(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	if client == nil {
		client = outbound.OutboundHTTPClient(20 * time.Second)
	}
	// Keep the application transport (DNS/IP checks), without forwarding session
	// cookies or following a public source to a different origin.
	public := *client
	public.Jar = nil
	public.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	outbound.ApplyDefaultOutboundHeaders(req)
	resp, err := public.Do(req)
	if err != nil {
		return nil, fmt.Errorf("读取 SLL %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SLL %s 返回 HTTP %d", req.URL.Path, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPublicDocument+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxPublicDocument {
		return nil, errors.New("SLL 公开响应过大")
	}
	return body, nil
}

func ParseMetrics(r io.Reader) (map[string]Metric, error) {
	var envelope struct {
		Success bool `json:"success"`
		Data    struct {
			Models []struct {
				ID          string   `json:"model_name"`
				Available   *bool    `json:"success_rate_available"`
				Rate        *float64 `json:"success_rate"`
				SampleCount *int64   `json:"sample_count"`
			} `json:"models"`
		} `json:"data"`
	}
	if err := decodePublic(r, &envelope); err != nil {
		return nil, err
	}
	if !envelope.Success || envelope.Data.Models == nil {
		return nil, errors.New("SLL 公开成功率响应不完整")
	}
	result := make(map[string]Metric, len(envelope.Data.Models))
	for _, row := range envelope.Data.Models {
		if _, exists := result[row.ID]; exists || row.ID == "" || row.Available == nil {
			return nil, errors.New("SLL 成功率记录标识或可用状态不明确")
		}
		if row.SampleCount != nil && *row.SampleCount < 0 {
			return nil, errors.New("SLL 成功率样本数无效")
		}
		metric := Metric{SampleCount: row.SampleCount}
		if *row.Available {
			if row.Rate == nil || *row.Rate < 0 || *row.Rate > 100 {
				return nil, errors.New("SLL 模型成功率数值无效")
			}
			metric.Rate = row.Rate
		}
		result[row.ID] = metric
	}
	return result, nil
}
