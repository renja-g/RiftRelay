package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name       string
		env        map[string]string
		wantErr    string
		assertions func(t *testing.T, cfg Config)
	}{
		{
			name: "loads defaults with required token",
			env: map[string]string{
				"RIOT_TOKEN": "token-a",
			},
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.Port != defaultPort {
					t.Fatalf("expected default port %d, got %d", defaultPort, cfg.Port)
				}
				if len(cfg.Tokens) != 1 || cfg.Tokens[0] != "token-a" {
					t.Fatalf("unexpected token parsing: %+v", cfg.Tokens)
				}
			},
		},
		{
			name: "parses typed overrides",
			env: map[string]string{
				"RIOT_TOKEN":                 "a,b",
				"PORT":                       "9101",
				"QUEUE_CAPACITY":             "1234",
				"ADMISSION_TIMEOUT":          "3s",
				"ADDITIONAL_WINDOW_SIZE":     "250ms",
				"ENABLE_METRICS":             "false",
				"ENABLE_PPROF":               "true",
				"TRANSPORT_MAX_IDLE_CONNS":   "111",
				"TRANSPORT_FORCE_HTTP2":      "false",
				"UPSTREAM_TIMEOUT":           "2s",
				"SERVER_READ_HEADER_TIMEOUT": "4s",
			},
			assertions: func(t *testing.T, cfg Config) {
				t.Helper()
				if cfg.Port != 9101 {
					t.Fatalf("expected port 9101, got %d", cfg.Port)
				}
				if cfg.QueueCapacity != 1234 {
					t.Fatalf("expected queue 1234, got %d", cfg.QueueCapacity)
				}
				if cfg.AdmissionTimeout != 3*time.Second {
					t.Fatalf("unexpected admission timeout: %s", cfg.AdmissionTimeout)
				}
				if cfg.AdditionalWindow != 250*time.Millisecond {
					t.Fatalf("unexpected additional window: %s", cfg.AdditionalWindow)
				}
				if cfg.MetricsEnabled {
					t.Fatalf("expected metrics disabled")
				}
				if !cfg.PprofEnabled {
					t.Fatalf("expected pprof enabled")
				}
				if cfg.UpstreamTransport.MaxIdleConns != 111 {
					t.Fatalf("expected max idle 111, got %d", cfg.UpstreamTransport.MaxIdleConns)
				}
				if cfg.UpstreamTransport.ForceAttemptHTTP2 {
					t.Fatalf("expected force http2 disabled")
				}
			},
		},
		{
			name:    "fails without token",
			env:     map[string]string{},
			wantErr: "RIOT_TOKEN env var is required",
		},
		{
			name: "fails on invalid integer and bool",
			env: map[string]string{
				"RIOT_TOKEN":     "token-a",
				"PORT":           "oops",
				"ENABLE_METRICS": "maybe",
			},
			wantErr: "PORT must be an integer",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			unset := applyEnv(t, tt.env)
			defer unset()

			cfg, err := Load()
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.assertions != nil {
				tt.assertions(t, cfg)
			}
		})
	}
}

func applyEnv(t *testing.T, values map[string]string) func() {
	t.Helper()

	keys := []string{
		"RIOT_TOKEN",
		"PORT",
		"QUEUE_CAPACITY",
		"ADMISSION_TIMEOUT",
		"ADDITIONAL_WINDOW_SIZE",
		"SHUTDOWN_TIMEOUT",
		"UPSTREAM_TIMEOUT",
		"ENABLE_METRICS",
		"ENABLE_PPROF",
		"SERVER_READ_HEADER_TIMEOUT",
		"SERVER_READ_TIMEOUT",
		"SERVER_WRITE_TIMEOUT",
		"SERVER_IDLE_TIMEOUT",
		"TRANSPORT_MAX_IDLE_CONNS",
		"TRANSPORT_MAX_IDLE_CONNS_PER_HOST",
		"TRANSPORT_MAX_CONNS_PER_HOST",
		"TRANSPORT_IDLE_CONN_TIMEOUT",
		"TRANSPORT_TLS_HANDSHAKE_TIMEOUT",
		"TRANSPORT_EXPECT_CONTINUE_TIMEOUT",
		"TRANSPORT_DIAL_TIMEOUT",
		"TRANSPORT_DIAL_KEEP_ALIVE",
		"TRANSPORT_RESPONSE_HEADER_TIMEOUT",
		"TRANSPORT_FORCE_HTTP2",
	}

	original := make(map[string]string, len(keys))
	present := make(map[string]bool, len(keys))
	for _, key := range keys {
		value, ok := os.LookupEnv(key)
		if ok {
			original[key] = value
			present[key] = true
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
	}

	for key, value := range values {
		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("set %s: %v", key, err)
		}
	}

	return func() {
		for _, key := range keys {
			if present[key] {
				_ = os.Setenv(key, original[key])
				continue
			}
			_ = os.Unsetenv(key)
		}
	}
}
