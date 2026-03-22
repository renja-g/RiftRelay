package httputil

import (
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		want     time.Duration
		wantOkay bool
	}{
		{name: "seconds", value: "3", want: 3 * time.Second, wantOkay: true},
		{name: "zero", value: "0", want: 0, wantOkay: true},
		{name: "trimmed", value: " 12 ", want: 12 * time.Second, wantOkay: true},
		{name: "empty", value: "", wantOkay: false},
		{name: "negative", value: "-1", wantOkay: false},
		{name: "nonnumeric", value: "later", wantOkay: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := ParseRetryAfter(tt.value)
			if ok != tt.wantOkay {
				t.Fatalf("ParseRetryAfter() ok = %v, want %v", ok, tt.wantOkay)
			}
			if got != tt.want {
				t.Fatalf("ParseRetryAfter() duration = %v, want %v", got, tt.want)
			}
		})
	}
}
