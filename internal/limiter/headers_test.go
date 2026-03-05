package limiter

import (
	"testing"
	"time"
)

func TestParseRateHeader(t *testing.T) {
	tests := []struct {
		name      string
		limit     string
		count     string
		wantLen   int
		wantFirst parsedWindow
	}{
		{
			name:    "single window",
			limit:   "20:1",
			count:   "5:1",
			wantLen: 1,
			wantFirst: parsedWindow{
				limit:  20,
				count:  5,
				window: time.Second,
			},
		},
		{
			name:    "multiple windows",
			limit:   "20:1,100:120",
			count:   "4:1,40:120",
			wantLen: 2,
			wantFirst: parsedWindow{
				limit:  20,
				count:  4,
				window: time.Second,
			},
		},
		{
			name:    "invalid returns empty",
			limit:   "broken",
			count:   "1:1",
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got := parseRateHeader(tt.limit, tt.count)
			if len(got) != tt.wantLen {
				t.Fatalf("expected %d entries, got %d", tt.wantLen, len(got))
			}
			if tt.wantLen > 0 {
				first := got[0]
				if first.limit != tt.wantFirst.limit || first.count != tt.wantFirst.count || first.window != tt.wantFirst.window {
					t.Fatalf("unexpected first window: %+v", first)
				}
			}
		})
	}
}
