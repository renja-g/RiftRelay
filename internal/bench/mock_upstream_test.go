package bench

import (
	"net/http"
	"testing"
)

func TestRegionFromHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want string
	}{
		{host: "europe.api.riotgames.com", want: "europe"},
		{host: "na1.api.riotgames.com", want: "na1"},
		{host: "kr.api.riotgames.com", want: "kr"},
		{host: "", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			if got := regionFromHost(tt.host); got != tt.want {
				t.Fatalf("regionFromHost(%q) = %q, want %q", tt.host, got, tt.want)
			}
		})
	}
}

func TestMockUpstreamReturnsRateLimitHeaders(t *testing.T) {
	t.Parallel()

	mock := NewMockUpstream()
	req := mustNewRequest(t, "https://euw1.api.riotgames.com/lol/league/v4/challengerleagues/by-queue/RANKED_SOLO_5x5")
	resp, err := mock.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got, want := resp.Header.Get("X-App-Rate-Limit"), appRateLimitHeader; got != want {
		t.Fatalf("X-App-Rate-Limit = %q, want %q", got, want)
	}
	if got, want := resp.Header.Get("X-Method-Rate-Limit"), methodRateLimitHeader; got != want {
		t.Fatalf("X-Method-Rate-Limit = %q, want %q", got, want)
	}
	if got := resp.Header.Get("X-App-Rate-Limit-Count"); got != "1:10" {
		t.Fatalf("X-App-Rate-Limit-Count = %q, want %q", got, "1:10")
	}
}

func mustNewRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("http.NewRequest() error = %v", err)
	}
	return req
}
