package limiter

import (
	"context"
	"net/http"
	"testing"
)

func BenchmarkLimiterAdmitNoLimits(b *testing.B) {
	l, err := New(Config{
		KeyCount:      2,
		QueueCapacity: 4096,
	})
	if err != nil {
		b.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	admission := Admission{
		Region:   "na1",
		Bucket:   "na1:lol/status/v4/platform-data",
		Priority: PriorityNormal,
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Admit(ctx, admission); err != nil {
			b.Fatalf("admit failed: %v", err)
		}
	}
}

func BenchmarkLimiterAdmitContention(b *testing.B) {
	l, err := New(Config{
		KeyCount:      1,
		QueueCapacity: 4096,
	})
	if err != nil {
		b.Fatalf("new limiter: %v", err)
	}
	defer l.Close()

	headers := make(http.Header)
	headers.Set("X-Method-Rate-Limit", "10:1")
	headers.Set("X-Method-Rate-Limit-Count", "0:1")
	l.Observe(Observation{
		Region:     "na1",
		Bucket:     "na1:lol/summoner/v4/summoners/by-name/foo",
		KeyIndex:   0,
		StatusCode: http.StatusOK,
		Header:     headers,
	})

	admission := Admission{
		Region:   "na1",
		Bucket:   "na1:lol/summoner/v4/summoners/by-name/foo",
		Priority: PriorityNormal,
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := l.Admit(ctx, admission); err != nil {
			b.Fatalf("admit failed: %v", err)
		}
	}
}
