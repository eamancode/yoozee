package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProviderMediaOrigin(t *testing.T) {
	cases := []struct {
		name string
		base string
		url  string
		want bool
	}{
		{"HTTPS default port", "https://API.example.test/api", "https://api.example.test:443/result", true},
		{"HTTP default port", "http://api.example.test:80", "http://api.example.test/result", true},
		{"same non-default port", "https://api.example.test:8443", "https://api.example.test:8443/result", true},
		{"different port", "https://api.example.test:8443", "https://api.example.test/result", false},
		{"different scheme", "https://api.example.test:443", "http://api.example.test:443/result", false},
		{"different host", "https://api.example.test", "https://cdn.example.test/result", false},
		{"subdomain", "https://api.example.test", "https://files.api.example.test/result", false},
		{"empty base", "", "https://cdn.example.test/result", false},
		{"relative target", "https://api.example.test", "/result", false},
	}
	for _, tc := range cases {
		if got := sameProviderOrigin(tc.base, tc.url); got != tc.want {
			t.Errorf("%s: sameProviderOrigin = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestProviderMediaDownloadCredentials(t *testing.T) {
	t.Setenv("CANVAS_ALLOWED_PRIVATE_UPSTREAM_HOSTS", "127.0.0.1")
	service, _ := newMediaRecoveryTestService(t)
	picture := mediaTestPNG(t)
	for _, protocol := range []struct {
		format, keyHeader, keyValue string
	}{
		{"openai", "Authorization", "Bearer fixture-key"},
		{"claude", "X-Api-Key", "fixture-key"},
		{"gemini", "X-Goog-Api-Key", "fixture-key"},
	} {
		t.Run(protocol.format, func(t *testing.T) {
			check := func(r *http.Request, sameOrigin bool) {
				t.Helper()
				for _, name := range []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "X-Channel-Access"} {
					want := ""
					if sameOrigin && name == protocol.keyHeader {
						want = protocol.keyValue
					}
					if sameOrigin && name == "X-Channel-Access" {
						want = "fixture-custom"
					}
					if r.Header.Get(name) != want {
						t.Errorf("%s received inappropriate %s header (same origin = %v)", r.URL.Path, name, sameOrigin)
					}
				}
			}
			writeMedia := func(w http.ResponseWriter) {
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write(picture)
			}
			const signed = "/signed?signature=fixture-signature"
			cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				check(r, false)
				if r.Header.Get("Referer") != "" {
					t.Errorf("CDN received a Referer at %s", r.URL.Path)
				}
				switch {
				case r.URL.Path == "/hop":
					http.Redirect(w, r, signed, http.StatusFound)
				case r.URL.Path == "/public", r.URL.RequestURI() == signed:
					writeMedia(w)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(cdn.Close)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				check(r, true)
				switch r.URL.Path {
				case "/same":
					http.Redirect(w, r, "/private", http.StatusFound)
				case "/cross":
					http.Redirect(w, r, cdn.URL+"/hop", http.StatusFound)
				case "/private":
					writeMedia(w)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(api.Close)
			cfg := providerConfig{BaseURL: api.URL + "/v1", APIFormat: protocol.format, APIKey: "fixture-key", Headers: []OutboundHeader{{Name: "X-Channel-Access", Value: "fixture-custom"}}}
			for _, target := range []struct {
				name, url string
			}{
				{"same-origin-redirect", api.URL + "/same"},
				{"public-other-port", cdn.URL + "/public"},
				{"signed-other-port", cdn.URL + signed},
				{"cross-origin-then-signed-redirect", api.URL + "/cross?source=fixture-source-signature"},
			} {
				t.Run(target.name, func(t *testing.T) {
					ctx := context.Background()
					name, mimeType, err := service.downloadTaskMedia(ctx, cfg, target.url, "image")
					if err != nil || name == "" || mimeType != "image/png" {
						t.Fatalf("stage result: name=%q type=%q err=%v", name, mimeType, err)
					}
					data, _, err := getProviderExternalBinary(ctx, cfg, target.url)
					if err != nil || !bytes.Equal(data, picture) {
						t.Fatalf("read provider result: bytes=%d err=%v", len(data), err)
					}
					if protocol.format == "gemini" {
						data, _, err = getGeminiBinary(ctx, cfg, target.url)
						if err != nil || !bytes.Equal(data, picture) {
							t.Fatalf("read Gemini result: bytes=%d err=%v", len(data), err)
						}
					}
				})
			}
		})
	}
}
