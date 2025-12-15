package transport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/renja-g/rp/internal/router"
)

func TestBuildKeyUsesPathInfo(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://example.com/na1/riot/some/path", nil)
	info := router.PathInfo{
		Region:      "na1",
		Path:        "/riot/some/path",
		PathPattern: "/riot/some/{id}",
	}
	ctx := router.WithPath(context.Background(), info)
	req = req.WithContext(ctx)

	got := buildKey(req)
	want := "na1|/riot/some/{id}"
	if got != want {
		t.Fatalf("buildKey() = %q, want %q", got, want)
	}
}
