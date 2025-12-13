package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name           string
		riotToken      string
		port           string
		wantToken      string
		wantPort       string
		wantMaxRetries int
		wantErr        bool
		wantErrMsg     string
	}{
		{
			name:           "all values provided",
			riotToken:      "test-token-123",
			port:           "9090",
			wantToken:      "test-token-123",
			wantPort:       "9090",
			wantMaxRetries: defaultMaxRetries,
			wantErr:        false,
		},
		{
			name:           "only token provided, port defaults",
			riotToken:      "my-secret-token",
			port:           "",
			wantToken:      "my-secret-token",
			wantPort:       defaultPort,
			wantMaxRetries: defaultMaxRetries,
			wantErr:        false,
		},
		{
			name:           "token and port provided",
			riotToken:      "token-456",
			port:           "3000",
			wantToken:      "token-456",
			wantPort:       "3000",
			wantMaxRetries: defaultMaxRetries,
			wantErr:        false,
		},
		{
			name:           "missing token should error",
			riotToken:      "",
			port:           "8080",
			wantToken:      "",
			wantPort:       "",
			wantMaxRetries: 0,
			wantErr:        true,
			wantErrMsg:     "RIOT_TOKEN env var is required",
		},
		{
			name:           "empty token string should error",
			riotToken:      "",
			port:           "",
			wantToken:      "",
			wantPort:       "",
			wantMaxRetries: 0,
			wantErr:        true,
			wantErrMsg:     "RIOT_TOKEN env var is required",
		},
		{
			name:           "whitespace-only token should error",
			riotToken:      "   ",
			port:           "8080",
			wantToken:      "",
			wantPort:       "",
			wantMaxRetries: 0,
			wantErr:        true,
			wantErrMsg:     "RIOT_TOKEN env var is required",
		},
		{
			name:           "port with whitespace gets trimmed",
			riotToken:      "valid-token",
			port:           "  9000  ",
			wantToken:      "valid-token",
			wantPort:       "9000",
			wantMaxRetries: defaultMaxRetries,
			wantErr:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalToken := os.Getenv("RIOT_TOKEN")
			originalPort := os.Getenv("PORT")
			defer func() {
				if originalToken != "" {
					os.Setenv("RIOT_TOKEN", originalToken)
				} else {
					os.Unsetenv("RIOT_TOKEN")
				}
				if originalPort != "" {
					os.Setenv("PORT", originalPort)
				} else {
					os.Unsetenv("PORT")
				}
			}()

			if tt.riotToken != "" {
				os.Setenv("RIOT_TOKEN", tt.riotToken)
			} else {
				os.Unsetenv("RIOT_TOKEN")
			}
			if tt.port != "" {
				os.Setenv("PORT", tt.port)
			} else {
				os.Unsetenv("PORT")
			}

			cfg, err := Load()

			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() error = nil, want error")
					return
				}
				if tt.wantErrMsg != "" && err.Error() != tt.wantErrMsg {
					t.Errorf("Load() error = %v, want %q", err, tt.wantErrMsg)
				}
				return
			}

			if err != nil {
				t.Fatalf("Load() error = %v, want nil", err)
			}
			if cfg.Token != tt.wantToken {
				t.Errorf("Load() Token = %q, want %q", cfg.Token, tt.wantToken)
			}
			if cfg.Port != tt.wantPort {
				t.Errorf("Load() Port = %q, want %q", cfg.Port, tt.wantPort)
			}
			if cfg.MaxRetries != tt.wantMaxRetries {
				t.Errorf("Load() MaxRetries = %d, want %d", cfg.MaxRetries, tt.wantMaxRetries)
			}
		})
	}
}
