package app

import (
	"context"
	"errors"
	"testing"
)

// 派普把视频结果发到同一 IP 的另一个域名（api.lecapi.net），
// 这类地址必须沿用渠道鉴权，否则 /v1/videos/{id}/content 一律 403。
func TestProviderHostRelated(t *testing.T) {
	original := lookupProviderHost
	t.Cleanup(func() { lookupProviderHost = original })

	hosts := map[string][]string{
		"api.paipu.net":   {"179.255.133.181"},
		"api.lecapi.net":  {"179.255.133.181"},
		"cdn.example.com": {"203.0.113.7"},
	}
	lookupProviderHost = func(_ context.Context, host string) ([]string, error) {
		if addrs, ok := hosts[host]; ok {
			return addrs, nil
		}
		return nil, errors.New("no such host")
	}

	cases := []struct {
		name string
		base string
		url  string
		want bool
	}{
		{"same origin", "https://api.paipu.net", "https://api.paipu.net/v1/videos/x/content", true},
		{"same ip other domain", "https://api.paipu.net", "https://api.lecapi.net/v1/videos/x/content", true},
		{"different server", "https://api.paipu.net", "https://cdn.example.com/x.mp4", false},
		{"unresolvable host", "https://api.paipu.net", "https://unknown.invalid/x.mp4", false},
		{"empty base", "", "https://api.lecapi.net/x.mp4", false},
	}
	for _, tc := range cases {
		if got := providerHostRelated(tc.base, tc.url); got != tc.want {
			t.Errorf("%s: providerHostRelated(%q, %q) = %v, want %v", tc.name, tc.base, tc.url, got, tc.want)
		}
	}
}
