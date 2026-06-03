package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

type loadTestCase struct {
	name      string
	env       map[string]string
	wantErr   []string
	assertCfg func(t *testing.T, cfg Config)
}

func TestLoad(t *testing.T) {
	tests := []loadTestCase{
		{
			name: "defaults with required token",
			env: map[string]string{
				"RIOT_TOKEN": "token-a, token-b",
			},
			assertCfg: assertLoadDefaults,
		},
		{
			name: "custom values",
			env: map[string]string{
				"RIOT_TOKEN":             "token-a",
				"PORT":                   "9001",
				"QUEUE_CAPACITY":         "42",
				"ADMISSION_TIMEOUT":      "3s",
				"ADDITIONAL_WINDOW_SIZE": "25ms",
				"SHUTDOWN_TIMEOUT":       "4s",
				"UPSTREAM_TIMEOUT":       "7s",
				"ENABLE_METRICS":         "false",
				"ENABLE_PPROF":           "true",
				"ENABLE_SWAGGER":         "false",
				"DEFAULT_APP_RATE_LIMIT": "10:1,40:120",
			},
			assertCfg: assertLoadCustomValues,
		},
		{
			name: "rate budgets",
			env: map[string]string{
				"RIOT_TOKEN":                   "token-a",
				"RATE_BUDGET_worker":           "0.8",
				"RATE_BUDGET_worker_OVERRIDES": "lol/match/v5/matches/{matchId}=0.6,europe:riot/account/v1/accounts/me=0.5",
			},
			assertCfg: assertLoadRateBudgets,
		},
		{
			name: "aggregates validation errors",
			env: map[string]string{
				"PORT":                   "70000",
				"QUEUE_CAPACITY":         "0",
				"ADMISSION_TIMEOUT":      "nope",
				"ENABLE_METRICS":         "sometimes",
				"DEFAULT_APP_RATE_LIMIT": "bad",
				"RATE_BUDGET_default":    "0.5",
				"RATE_BUDGET_worker":     "1.5",
			},
			wantErr: []string{
				"RIOT_TOKEN env var is required",
				"PORT must be <= 65535",
				"QUEUE_CAPACITY must be >= 1",
				"ADMISSION_TIMEOUT must be a valid duration",
				"ENABLE_METRICS must be a boolean",
				"DEFAULT_APP_RATE_LIMIT must be in format",
				"RATE_BUDGET_default has invalid budget id",
				"RATE_BUDGET_worker must be a number > 0 and <= 1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runLoadTestCase(t, tt)
		})
	}
}

func runLoadTestCase(t *testing.T, tt loadTestCase) {
	t.Helper()
	clearConfigEnv(t)
	for key, value := range tt.env {
		t.Setenv(key, value)
	}

	cfg, err := Load()
	if len(tt.wantErr) > 0 {
		assertLoadErrors(t, err, tt.wantErr)
		return
	}
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if tt.assertCfg != nil {
		tt.assertCfg(t, cfg)
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"RIOT_TOKEN",
		"PORT",
		"QUEUE_CAPACITY",
		"ADMISSION_TIMEOUT",
		"ADDITIONAL_WINDOW_SIZE",
		"SHUTDOWN_TIMEOUT",
		"UPSTREAM_TIMEOUT",
		"ENABLE_METRICS",
		"ENABLE_PPROF",
		"ENABLE_SWAGGER",
		"DEFAULT_APP_RATE_LIMIT",
	} {
		t.Setenv(key, "")
	}
	for _, env := range os.Environ() {
		key, _, ok := strings.Cut(env, "=")
		if ok && strings.HasPrefix(key, "RATE_BUDGET_") {
			t.Setenv(key, "")
		}
	}
}

func assertLoadErrors(t *testing.T, err error, wantErr []string) {
	t.Helper()
	if err == nil {
		t.Fatal("Load() error = nil, want non-nil")
	}
	for _, fragment := range wantErr {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("Load() error = %q, want substring %q", err.Error(), fragment)
		}
	}
}

func assertLoadDefaults(t *testing.T, cfg Config) {
	t.Helper()
	if got, want := cfg.Tokens, []string{"token-a", "token-b"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("Tokens = %v, want %v", got, want)
	}
	if got, want := cfg.Port, defaultPort; got != want {
		t.Fatalf("Port = %d, want %d", got, want)
	}
	if got, want := cfg.Server.WriteTimeout, defaultAdmissionTimeout+5*time.Minute+30*time.Second; got != want {
		t.Fatalf("Server.WriteTimeout = %v, want %v", got, want)
	}
	if len(cfg.RateBudgets) != 0 {
		t.Fatalf("RateBudgets = %v, want empty", cfg.RateBudgets)
	}
}

func assertLoadCustomValues(t *testing.T, cfg Config) {
	t.Helper()
	if got, want := cfg.Port, 9001; got != want {
		t.Fatalf("Port = %d, want %d", got, want)
	}
	if got, want := cfg.QueueCapacity, 42; got != want {
		t.Fatalf("QueueCapacity = %d, want %d", got, want)
	}
	if got, want := cfg.UpstreamTimeout, 7*time.Second; got != want {
		t.Fatalf("UpstreamTimeout = %v, want %v", got, want)
	}
	if cfg.MetricsEnabled {
		t.Fatal("MetricsEnabled = true, want false")
	}
	if !cfg.PprofEnabled {
		t.Fatal("PprofEnabled = false, want true")
	}
	if cfg.SwaggerEnabled {
		t.Fatal("SwaggerEnabled = true, want false")
	}
	if got, want := cfg.Server.WriteTimeout, 3*time.Second+7*time.Second+30*time.Second; got != want {
		t.Fatalf("Server.WriteTimeout = %v, want %v", got, want)
	}
}

func assertLoadRateBudgets(t *testing.T, cfg Config) {
	t.Helper()
	if got, want := cfg.RateBudgets["worker"].Share, 0.8; got != want {
		t.Fatalf("worker share = %v, want %v", got, want)
	}
	share, ok := cfg.RateBudgetShare("worker", "europe:lol/match/v5/matches/{matchId}")
	if !ok {
		t.Fatal("RateBudgetShare() ok = false, want true")
	}
	if got, want := share, 0.6; got != want {
		t.Fatalf("RateBudgetShare() = %v, want %v", got, want)
	}
	share, ok = cfg.RateBudgetShare("worker", "europe:riot/account/v1/accounts/me")
	if !ok {
		t.Fatal("RateBudgetShare() ok = false, want true")
	}
	if got, want := share, 0.5; got != want {
		t.Fatalf("RateBudgetShare() = %v, want %v", got, want)
	}
}
