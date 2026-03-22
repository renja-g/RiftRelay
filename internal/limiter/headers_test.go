package limiter

import (
	"testing"
	"time"
)

func TestParseRateHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		limitHeader string
		countHeader string
		want        []parsedWindow
	}{
		{
			name:        "parses aligned limits and counts",
			limitHeader: "20:1,100:120",
			countHeader: "3:1,70:120",
			want: []parsedWindow{
				{limit: 20, count: 3, window: time.Second},
				{limit: 100, count: 70, window: 120 * time.Second},
			},
		},
		{
			name:        "missing counts default to zero",
			limitHeader: "10:10,20:20",
			countHeader: "",
			want: []parsedWindow{
				{limit: 10, count: 0, window: 10 * time.Second},
				{limit: 20, count: 0, window: 20 * time.Second},
			},
		},
		{
			name:        "skips invalid entries",
			limitHeader: "bad,5:1,3:0",
			countHeader: "9:1,2:1",
			want: []parsedWindow{
				{limit: 5, count: 2, window: time.Second},
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := parseRateHeader(tt.limitHeader, tt.countHeader)
			if len(got) != len(tt.want) {
				t.Fatalf("len(parseRateHeader()) = %d, want %d", len(got), len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("parseRateHeader()[%d] = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
