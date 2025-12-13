package router

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestShiftPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantInfo PathInfo
		wantOk   bool
	}{
		{
			name:     "empty path",
			input:    "",
			wantInfo: PathInfo{},
			wantOk:   false,
		},
		{
			name:     "root path",
			input:    "/",
			wantInfo: PathInfo{},
			wantOk:   false,
		},
		{
			name:  "only region",
			input: "/na1",
			wantInfo: PathInfo{
				Region:      "na1",
				Path:        "/",
				PathPattern: "",
			},
			wantOk: true,
		},
		{
			name:  "europe region with riot account by-riot-id",
			input: "/europe/riot/account/v1/accounts/by-riot-id/Ayato/11235",
			wantInfo: PathInfo{
				Region:      "europe",
				Path:        "/riot/account/v1/accounts/by-riot-id/Ayato/11235",
				PathPattern: "/riot/account/v1/accounts/by-riot-id/{gameName}/{tagLine}",
			},
			wantOk: true,
		},
		{
			name:  "na1 region with summoner me",
			input: "/na1/lol/summoner/v4/summoners/me",
			wantInfo: PathInfo{
				Region:      "na1",
				Path:        "/lol/summoner/v4/summoners/me",
				PathPattern: "/lol/summoner/v4/summoners/me",
			},
			wantOk: true,
		},
		{
			name:  "region with parameterized path",
			input: "/euw1/lol/summoner/v4/summoners/by-puuid/abc123",
			wantInfo: PathInfo{
				Region:      "euw1",
				Path:        "/lol/summoner/v4/summoners/by-puuid/abc123",
				PathPattern: "/lol/summoner/v4/summoners/by-puuid/{encryptedPUUID}",
			},
			wantOk: true,
		},
		{
			name:  "region with multi-segment path",
			input: "/kr/lol/challenges/v1/challenges/123/leaderboards/by-level/5",
			wantInfo: PathInfo{
				Region:      "kr",
				Path:        "/lol/challenges/v1/challenges/123/leaderboards/by-level/5",
				PathPattern: "/lol/challenges/v1/challenges/{challengeId}/leaderboards/by-level/{level}",
			},
			wantOk: true,
		},
		{
			name:  "path without leading slash",
			input: "na1/lol/status/v4/platform-data",
			wantInfo: PathInfo{
				Region:      "na1",
				Path:        "/lol/status/v4/platform-data",
				PathPattern: "/lol/status/v4/platform-data",
			},
			wantOk: true,
		},
		{
			name:  "region with unmatched path",
			input: "/br1/unknown/path/that/does/not/match",
			wantInfo: PathInfo{
				Region:      "br1",
				Path:        "/unknown/path/that/does/not/match",
				PathPattern: "",
			},
			wantOk: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInfo, gotOk := ShiftPath(tt.input)
			if gotOk != tt.wantOk {
				t.Errorf("ShiftPath() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotInfo.Region != tt.wantInfo.Region {
				t.Errorf("ShiftPath() Region = %v, want %v", gotInfo.Region, tt.wantInfo.Region)
			}
			if gotInfo.Path != tt.wantInfo.Path {
				t.Errorf("ShiftPath() Path = %v, want %v", gotInfo.Path, tt.wantInfo.Path)
			}
			if gotInfo.PathPattern != tt.wantInfo.PathPattern {
				t.Errorf("ShiftPath() PathPattern = %v, want %v", gotInfo.PathPattern, tt.wantInfo.PathPattern)
			}
		})
	}
}

func TestFindMatchingPattern(t *testing.T) {
	tests := []struct {
		name    string
		reqPath string
		want    string
	}{
		{
			name:    "exact match",
			reqPath: "/lol/status/v4/platform-data",
			want:    "/lol/status/v4/platform-data",
		},
		{
			name:    "parameterized match single param",
			reqPath: "/lol/summoner/v4/summoners/by-puuid/abc123def456",
			want:    "/lol/summoner/v4/summoners/by-puuid/{encryptedPUUID}",
		},
		{
			name:    "parameterized match multiple params",
			reqPath: "/lol/challenges/v1/challenges/12345/leaderboards/by-level/5",
			want:    "/lol/challenges/v1/challenges/{challengeId}/leaderboards/by-level/{level}",
		},
		{
			name:    "parameterized match league entry",
			reqPath: "/lol/league/v4/entries/RANKED_SOLO_5x5/DIAMOND/I",
			want:    "/lol/league/v4/entries/{queue}/{tier}/{division}",
		},
		{
			name:    "parameterized match account by riot id",
			reqPath: "/riot/account/v1/accounts/by-riot-id/PlayerName/TAG123",
			want:    "/riot/account/v1/accounts/by-riot-id/{gameName}/{tagLine}",
		},
		{
			name:    "no match",
			reqPath: "/unknown/path/that/does/not/exist",
			want:    "",
		},
		{
			name:    "empty path",
			reqPath: "",
			want:    "",
		},
		{
			name:    "root path",
			reqPath: "/",
			want:    "",
		},
		{
			name:    "wrong segment count",
			reqPath: "/lol/summoner/v4/summoners/by-puuid/abc123/extra",
			want:    "",
		},
		{
			name:    "match with literal segments",
			reqPath: "/lol/match/v5/matches/NA1_1234567890",
			want:    "/lol/match/v5/matches/{matchId}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := findMatchingPattern(tt.reqPath)
			if got != tt.want {
				t.Errorf("findMatchingPattern() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWithPath(t *testing.T) {
	ctx := context.Background()
	info := PathInfo{
		Region:      "na1",
		Path:        "/lol/status/v4/platform-data",
		PathPattern: "/lol/status/v4/platform-data",
	}

	ctxWithPath := WithPath(ctx, info)

	// Verify the path info can be retrieved
	gotInfo, ok := PathFromContext(ctxWithPath)
	if !ok {
		t.Fatal("PathFromContext() ok = false, want true")
	}
	if gotInfo.Region != info.Region {
		t.Errorf("PathFromContext() Region = %v, want %v", gotInfo.Region, info.Region)
	}
	if gotInfo.Path != info.Path {
		t.Errorf("PathFromContext() Path = %v, want %v", gotInfo.Path, info.Path)
	}
	if gotInfo.PathPattern != info.PathPattern {
		t.Errorf("PathFromContext() PathPattern = %v, want %v", gotInfo.PathPattern, info.PathPattern)
	}
}

func TestPathFromContext(t *testing.T) {
	tests := []struct {
		name     string
		ctx      context.Context
		wantInfo PathInfo
		wantOk   bool
	}{
		{
			name:     "context with path info",
			ctx:      WithPath(context.Background(), PathInfo{Region: "euw1", Path: "/lol/status/v4/platform-data", PathPattern: "/lol/status/v4/platform-data"}),
			wantInfo: PathInfo{Region: "euw1", Path: "/lol/status/v4/platform-data", PathPattern: "/lol/status/v4/platform-data"},
			wantOk:   true,
		},
		{
			name:     "empty context",
			ctx:      context.Background(),
			wantInfo: PathInfo{},
			wantOk:   false,
		},
		{
			name:     "context with different value type",
			ctx:      context.WithValue(context.Background(), pathContextKey{}, "not a PathInfo"),
			wantInfo: PathInfo{},
			wantOk:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotInfo, gotOk := PathFromContext(tt.ctx)
			if gotOk != tt.wantOk {
				t.Errorf("PathFromContext() ok = %v, want %v", gotOk, tt.wantOk)
			}
			if gotInfo.Region != tt.wantInfo.Region {
				t.Errorf("PathFromContext() Region = %v, want %v", gotInfo.Region, tt.wantInfo.Region)
			}
			if gotInfo.Path != tt.wantInfo.Path {
				t.Errorf("PathFromContext() Path = %v, want %v", gotInfo.Path, tt.wantInfo.Path)
			}
			if gotInfo.PathPattern != tt.wantInfo.PathPattern {
				t.Errorf("PathFromContext() PathPattern = %v, want %v", gotInfo.PathPattern, tt.wantInfo.PathPattern)
			}
		})
	}
}

func TestProxyHandler(t *testing.T) {
	tests := []struct {
		name           string
		path           string
		wantStatusCode int
		wantBody       string
		shouldCallNext bool
	}{
		{
			name:           "valid path with region",
			path:           "/na1/lol/status/v4/platform-data",
			wantStatusCode: http.StatusOK,
			wantBody:       "proxied",
			shouldCallNext: true,
		},
		{
			name:           "empty path",
			path:           "/",
			wantStatusCode: http.StatusBadRequest,
			wantBody:       "expected path /{region}/riot/...\n",
			shouldCallNext: false,
		},
		{
			name:           "only region",
			path:           "/na1",
			wantStatusCode: http.StatusBadRequest,
			wantBody:       "expected path /{region}/riot/...\n",
			shouldCallNext: false,
		},
		{
			name:           "path is root after region",
			path:           "/na1/",
			wantStatusCode: http.StatusBadRequest,
			wantBody:       "expected path /{region}/riot/...\n",
			shouldCallNext: false,
		},
		{
			name:           "valid path with parameterized route",
			path:           "/euw1/lol/summoner/v4/summoners/by-puuid/abc123",
			wantStatusCode: http.StatusOK,
			wantBody:       "proxied",
			shouldCallNext: true,
		},
		{
			name:           "valid path with different region",
			path:           "/kr/lol/status/v4/platform-data",
			wantStatusCode: http.StatusOK,
			wantBody:       "proxied",
			shouldCallNext: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				// Verify path info is in context
				info, ok := PathFromContext(r.Context())
				if !ok {
					t.Error("PathFromContext() ok = false, want true")
				}
				if info.Path == "/" {
					t.Error("PathFromContext() Path = '/', should not be root")
				}
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("proxied"))
			})

			handler := ProxyHandler(nextHandler)
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatusCode {
				t.Errorf("ProxyHandler() status code = %v, want %v", rec.Code, tt.wantStatusCode)
			}
			if rec.Body.String() != tt.wantBody {
				t.Errorf("ProxyHandler() body = %v, want %v", rec.Body.String(), tt.wantBody)
			}
			if nextCalled != tt.shouldCallNext {
				t.Errorf("ProxyHandler() next called = %v, want %v", nextCalled, tt.shouldCallNext)
			}
		})
	}
}
