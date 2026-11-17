package sllcatalog

import (
	"context"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func parseFixture(t *testing.T, pricing, media, status, group string) Catalog {
	t.Helper()
	got, err := ParsePricing(strings.NewReader(pricing), strings.NewReader(media), strings.NewReader(status), group)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestSourcePublicPriceAndSubmitCapabilities(t *testing.T) {
	price, media, status := fixture(t, "pricing"), fixture(t, "media"), fixture(t, "status")
	catalog := parseFixture(t, price, media, status, "default")
	if len(catalog.Entries) != 2 || len(catalog.Excluded) != 0 {
		t.Fatalf("catalog = %+v", catalog)
	}
	hc, fixed := catalog.Entries[0], catalog.Entries[1]
	if len(hc.Tiers) != 2 || hc.Tiers[1].CostMicroCNY != 6_000_000 || hc.Capabilities.DurationMin != 4 ||
		hc.Capabilities.DurationMax != 30 || *hc.Capabilities.MaxVideos != 0 || *hc.Capabilities.MaxAudios != 10 ||
		!hc.Capabilities.SendDuration || !hc.Capabilities.SendResolution || hc.Capabilities.GenerateAudio != nil {
		t.Fatalf("HC = %+v", hc)
	}
	c := fixed.Capabilities
	if !slices.Equal(c.Durations, []int{30}) || c.SendDuration || c.SendResolution || c.MinImages != 1 ||
		c.FixedResolution != "720p" || c.MaxImageBytes != 15728640 || !slices.Contains(c.AudioMIMEs, "audio/wav") {
		t.Fatalf("fixed = %+v", c)
	}
	vip := parseFixture(t, price, media, status, "vip")
	if len(vip.Entries) != 1 || len(vip.Entries[0].Tiers) != 1 || vip.Entries[0].Tiers[0].CostMicroCNY != 6_000_000 || len(vip.Excluded) != 2 {
		t.Fatalf("VIP = %+v", vip)
	}
	sameName := strings.Replace(price, fixed.DisplayName, hc.DisplayName, 1)
	result := parseFixture(t, sameName, media, status, "default")
	if len(result.Entries) != 2 || result.Entries[0].ModelID == result.Entries[1].ModelID || result.Entries[0].DisplayName != result.Entries[1].DisplayName {
		t.Fatalf("same display name = %+v", result)
	}
}

func TestSourceExcludesUnverifiedTiers(t *testing.T) {
	origPrice, origMedia, origStatus := fixture(t, "pricing"), fixture(t, "media"), fixture(t, "status")
	for _, tc := range []struct {
		name, doc, from, to string
		wantTiers           int
	}{
		{"above budget", "pricing", `"model_price": 6}`, `"model_price": 6.01}`, 1},
		{"strict decimal boundary", "pricing", `"model_price": 6}`, `"model_price": 6.000000000000000001}`, 1},
		{"seconds", "pricing", `"billing_unit": "request"`, `"billing_unit": "second"`, 1},
		{"unknown tier currency", "pricing", `"billing_unit": "request"`, `"billing_unit": "request","currency":"USD"`, 1},
		{"duplicated resolution", "pricing", `"resolution": "480p"`, `"resolution": "720p"`, 0},
		{"duration dependent tier", "pricing", `"billing_unit": "request"`, `"billing_unit": "request","duration_seconds":15`, 1},
		{"unknown group factor", "pricing", `"default": 1,`, `"unused": 1,`, 0},
		{"different family", "media", `"family_id": "seedance-2.5"`, `"family_id": "seedance-2.0"`, 0},
		{"unroutable", "media", `"routable": true`, `"routable": false`, 0},
		{"unselectable resolution", "media", `"name": "resolution"`, `"name": "other"`, 0},
		{"unverified display currency", "status", `"CNY"`, `"USD"`, 0},
		{"unverified display conversion", "status", `"usd_exchange_rate":1`, `"usd_exchange_rate":7`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			price, media, status := origPrice, origMedia, origStatus
			target := map[string]*string{"pricing": &price, "media": &media, "status": &status}[tc.doc]
			if !strings.Contains(*target, tc.from) {
				t.Fatal("fixture drift")
			}
			*target = strings.Replace(*target, tc.from, tc.to, 1)
			catalog := parseFixture(t, price, media, status, "default")
			tiers := 0
			for _, entry := range catalog.Entries {
				if entry.ModelID == "lec-hc-seedance-2-5-3010" {
					tiers = len(entry.Tiers)
				}
			}
			if tiers != tc.wantTiers || len(catalog.Excluded) == 0 {
				t.Fatalf("catalog = %+v", catalog)
			}
		})
	}
	roundUp := strings.Replace(origPrice, `"model_price": 6}`, `"model_price": 5.00000001}`, 1)
	if micro := parseFixture(t, roundUp, origMedia, origStatus, "default").Entries[0].Tiers[1].CostMicroCNY; micro != 5_000_001 {
		t.Fatalf("micro = %d", micro)
	}
	discrete := strings.Replace(origMedia, `"min": 4, "max": 30, "step": 1`, `"values": [5,10,15,30]`, 1)
	noVideos := strings.Replace(discrete, `"max_reference_videos": 0,`, "", 1)
	caps := parseFixture(t, origPrice, noVideos, origStatus, "default").Entries[0].Capabilities
	if !slices.Equal(caps.Durations, []int{5, 10, 15, 30}) || caps.MaxVideos != nil {
		t.Fatalf("discrete / unreported = %+v", caps)
	}
}

func TestSourceMetricsZeroMissingAndMalformed(t *testing.T) {
	raw := fixture(t, "metrics")
	m, err := ParseMetrics(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	zero, missing := m["lec-hc-seedance-2-5-3010"], m["lec-seedance-2-5-301010"]
	if zero.Rate == nil || *zero.Rate != 0 || zero.SampleCount != nil || missing.Rate != nil {
		t.Fatalf("metrics = %+v", m)
	}
	for _, doc := range []string{`{"success":false}`, `{"success":true,"data":{}}`, raw + "oops",
		strings.Replace(raw, `"success_rate": 0`, `"success_rate": 101`, 1),
		strings.Replace(raw, "lec-seedance-2-5-301010", "lec-hc-seedance-2-5-3010", 1)} {
		if _, err := ParseMetrics(strings.NewReader(doc)); err == nil {
			t.Errorf("accepted invalid metrics %s", doc)
		}
	}
}

type sourceTransport func(*http.Request) (*http.Response, error)

func (f sourceTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestSourceFetchOnlyPublicMetadata(t *testing.T) {
	var paths []string
	mapping := map[string]string{PricingURL: "pricing", MediaURL: "media", StatusURL: "status", MetricsURL: "metrics"}
	client := &http.Client{Transport: sourceTransport(func(r *http.Request) (*http.Response, error) {
		name, ok := mapping[r.URL.String()]
		if !ok || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Fatalf("unexpected request %v", r)
		}
		paths = append(paths, name)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(fixture(t, name)))}, nil
	})}
	if _, err := FetchPricing(context.Background(), client, "default"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(paths, []string{"pricing", "media", "status"}) {
		t.Fatalf("catalog requests = %v", paths)
	}
	paths = nil
	if _, err := FetchMetrics(context.Background(), client); err != nil || !slices.Equal(paths, []string{"metrics"}) {
		t.Fatalf("metrics requests = %v, %v", paths, err)
	}
	client.Transport = sourceTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("unavailable"))}, nil
	})
	if _, err := FetchMetrics(context.Background(), client); err == nil {
		t.Fatal("503 counted as unknown metrics")
	}
}

func TestSourceFailsClosedOnMalformedCatalog(t *testing.T) {
	price, media, status := fixture(t, "pricing"), fixture(t, "media"), fixture(t, "status")
	for _, input := range []string{price + "null", strings.Replace(price, "true", "false", 1),
		strings.ReplaceAll(price, "lec-seedance-2-5-301010", "lec-hc-seedance-2-5-3010")} {
		if _, err := ParsePricing(strings.NewReader(input), strings.NewReader(media), strings.NewReader(status), "default"); err == nil {
			t.Fatal("malformed publication accepted")
		}
	}
}
