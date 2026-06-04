package bench

import (
	"testing"
)

func TestThroughputSingleRegion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sustained throughput test in -short mode")
	}

	mock := NewMockUpstream()
	relay, cleanup := newBenchServer(t, mock)
	defer cleanup()

	parallel := defaultSingleRegionParallelism()
	result := runLoad(t, relay, loadConfig{
		path:     singleRegionPath,
		warmup:   defaultBenchWarmup,
		duration: defaultBenchDuration,
		parallel: parallel,
		inFlight: parallel * 2,
	})
	logLoadResult(t, "single_region/europe", result, parallel)
}

func TestThroughputLeagueV4AllPlatforms(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping sustained throughput test in -short mode")
	}

	mock := NewMockUpstream()
	relay, cleanup := newBenchServer(t, mock)
	defer cleanup()

	parallel := defaultLeagueParallelism()
	result := runLoad(t, relay, loadConfig{
		path:     leagueV4PlatformPath,
		warmup:   defaultBenchWarmup,
		duration: defaultBenchDuration,
		parallel: parallel,
		inFlight: parallel * 2,
	})
	logLoadResult(t, "league_v4_all_platforms", result, parallel)
}

func BenchmarkThroughputSingleRegion(b *testing.B) {
	mock := NewMockUpstream()
	relay, cleanup := newBenchServer(b, mock)
	defer cleanup()

	b.ResetTimer()
	parallel := defaultSingleRegionParallelism()
	result := runLoad(b, relay, loadConfig{
		path:     singleRegionPath,
		warmup:   defaultBenchWarmup,
		duration: defaultBenchDuration,
		parallel: parallel,
		inFlight: parallel * 2,
	})
	reportBenchMetrics(b, result)
}

func BenchmarkThroughputLeagueV4AllPlatforms(b *testing.B) {
	mock := NewMockUpstream()
	relay, cleanup := newBenchServer(b, mock)
	defer cleanup()

	b.ResetTimer()
	parallel := defaultLeagueParallelism()
	result := runLoad(b, relay, loadConfig{
		path:     leagueV4PlatformPath,
		warmup:   defaultBenchWarmup,
		duration: defaultBenchDuration,
		parallel: parallel,
		inFlight: parallel * 2,
	})
	reportBenchMetrics(b, result)
}
