package router

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParsePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		rawPath string
		want    PathInfo
		wantErr bool
	}{
		{
			name:    "matches known route pattern",
			rawPath: "/europe/riot/account/v1/accounts/by-riot-id/Someone/EUW1",
			want: PathInfo{
				Region:       "europe",
				UpstreamPath: "/riot/account/v1/accounts/by-riot-id/Someone/EUW1",
				Bucket:       "europe:riot/account/v1/accounts/by-riot-id/{gameName}/{tagLine}",
			},
		},
		{
			name:    "falls back to concrete upstream path when no pattern matches",
			rawPath: "/na1/custom/endpoint",
			want: PathInfo{
				Region:       "na1",
				UpstreamPath: "/custom/endpoint",
				Bucket:       "na1:custom/endpoint",
			},
		},
		{
			name:    "cleans path traversal segments",
			rawPath: "/na1/riot/account/v1/../v1/accounts/me",
			want: PathInfo{
				Region:       "na1",
				UpstreamPath: "/riot/account/v1/accounts/me",
				Bucket:       "na1:riot/account/v1/accounts/me",
			},
		},
		{name: "missing region", rawPath: "/", wantErr: true},
		{name: "missing upstream path", rawPath: "/europe", wantErr: true},
		{name: "invalid region", rawPath: "/EU West/riot/account/v1/accounts/me", wantErr: true},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParsePath(tt.rawPath)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ParsePath() error = nil, want non-nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePath() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("ParsePath() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestProxyHandler(t *testing.T) {
	t.Parallel()

	t.Run("injects parsed path into request context", func(t *testing.T) {
		t.Parallel()

		var got PathInfo
		handler := ProxyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			info, ok := PathFromContext(r.Context())
			if !ok {
				t.Fatal("PathFromContext() ok = false, want true")
			}
			got = info
			w.WriteHeader(http.StatusNoContent)
		}))

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusNoContent; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got.Region != "europe" || got.UpstreamPath != "/riot/account/v1/accounts/me" {
			t.Fatalf("got PathInfo = %#v", got)
		}
	})

	t.Run("rejects invalid paths", func(t *testing.T) {
		t.Parallel()

		handler := ProxyHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Fatal("inner handler should not be called")
		}))

		req := httptest.NewRequest(http.MethodGet, "/invalid", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadRequest; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}
