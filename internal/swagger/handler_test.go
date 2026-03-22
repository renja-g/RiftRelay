package swagger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/renja-g/RiftRelay/internal/testutil"
)

func TestHandlerServeHTTP(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("testdata", "openapi.json")
	fixture, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) error = %v", fixturePath, err)
	}

	t.Run("serves swagger ui", func(t *testing.T) {
		t.Parallel()

		handler := NewHandlerWithClient("https://example.invalid/openapi.json", &http.Client{
			Transport: testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				t.Fatal("spec client should not be used for UI route")
				return nil, nil
			}),
		})

		req := httptest.NewRequest(http.MethodGet, "/swagger/", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if !strings.Contains(rec.Body.String(), `url: "/swagger/openapi.json"`) {
			t.Fatalf("UI body = %q, want embedded spec path", rec.Body.String())
		}
	})

	t.Run("rewrites upstream spec offline", func(t *testing.T) {
		t.Parallel()

		handler := NewHandlerWithClient("https://example.invalid/openapi.json", &http.Client{
			Transport: testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return testutil.HTTPResponseBytes(http.StatusOK, fixture, http.Header{
					"Content-Type": []string{"application/json"},
				}), nil
			}),
		})

		req := httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil)
		req.Host = "relay.local"
		req.Header.Set("X-Forwarded-Proto", "https")
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusOK; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}

		var doc map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
			t.Fatalf("json.Unmarshal() error = %v", err)
		}

		if _, ok := doc["security"]; ok {
			t.Fatal("doc.security exists, want stripped")
		}

		servers := doc["servers"].([]any)
		server := servers[0].(map[string]any)
		if got, want := server["url"], "https://relay.local/{region}"; got != want {
			t.Fatalf("server url = %v, want %v", got, want)
		}

		paths := doc["paths"].(map[string]any)
		pathItem := paths["/riot/account/v1/accounts/me"].(map[string]any)
		getOp := pathItem["get"].(map[string]any)
		if _, ok := getOp["security"]; ok {
			t.Fatal("operation security exists, want stripped")
		}

		parameters := getOp["parameters"].([]any)
		foundPriority := 0
		for _, raw := range parameters {
			param := raw.(map[string]any)
			if param["name"] == priorityHeaderName {
				foundPriority++
			}
		}
		if got, want := foundPriority, 1; got != want {
			t.Fatalf("priority header count = %d, want %d", got, want)
		}
	})

	t.Run("maps bad upstream payloads to bad gateway", func(t *testing.T) {
		t.Parallel()

		handler := NewHandlerWithClient("https://example.invalid/openapi.json", &http.Client{
			Transport: testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return testutil.HTTPResponse(http.StatusOK, "{not json", nil), nil
			}),
		})

		req := httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if got, want := rec.Code, http.StatusBadGateway; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}
