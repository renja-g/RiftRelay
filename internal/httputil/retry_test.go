package httputil

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		wantWait time.Duration
		wantOK   bool
	}{
		{
			name:     "delta seconds",
			value:    "2",
			wantWait: 2 * time.Second,
			wantOK:   true,
		},
		{
			name:     "zero seconds",
			value:    "0",
			wantWait: 0,
			wantOK:   true,
		},
		{
			name:     "delta seconds three",
			value:    "3",
			wantWait: 3 * time.Second,
			wantOK:   true,
		},
		{
			name:   "invalid value",
			value:  "invalid",
			wantOK: false,
		},
		{
			name:   "invalid header",
			value:  "later",
			wantOK: false,
		},
		{
			name:   "empty header",
			value:  "",
			wantOK: false,
		},
		{
			name:   "negative seconds are invalid",
			value:  "-1",
			wantOK: false,
		},
		{
			name:     "whitespace trimmed",
			value:    "  5  ",
			wantWait: 5 * time.Second,
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			gotWait, gotOK := ParseRetryAfter(tt.value)
			if gotOK != tt.wantOK {
				t.Fatalf("expected ok=%v, got %v", tt.wantOK, gotOK)
			}
			if gotWait != tt.wantWait {
				t.Fatalf("expected wait=%s, got %s", tt.wantWait, gotWait)
			}
		})
	}
}
