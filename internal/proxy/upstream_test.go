package proxy

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
	"github.com/renja-g/RiftRelay/internal/testutil"
)

const ladderPath = "/euw1/lol/league/v4/entries/RANKED_SOLO_5x5/GOLD/IV?page=1570"

// ladderPage builds a body shaped like a Riot league-v4 entries page.
func ladderPage(tb testing.TB) []byte {
	tb.Helper()

	type entry struct {
		LeagueID     string `json:"leagueId"`
		QueueType    string `json:"queueType"`
		Tier         string `json:"tier"`
		Rank         string `json:"rank"`
		PUUID        string `json:"puuid"`
		LeaguePoints int    `json:"leaguePoints"`
		Wins         int    `json:"wins"`
		Losses       int    `json:"losses"`
		Veteran      bool   `json:"veteran"`
		Inactive     bool   `json:"inactive"`
		FreshBlood   bool   `json:"freshBlood"`
		HotStreak    bool   `json:"hotStreak"`
	}
	entries := make([]entry, 205)
	for i := range entries {
		entries[i] = entry{
			LeagueID:     fmt.Sprintf("%08x-1d2c-4b8e-9f3a-%012x", i*7919, i*104729),
			QueueType:    "RANKED_SOLO_5x5",
			Tier:         "GOLD",
			Rank:         "IV",
			PUUID:        strings.Repeat(fmt.Sprintf("%03x", i), 26),
			LeaguePoints: i % 100,
			Wins:         20 + i%150,
			Losses:       18 + i%140,
			HotStreak:    i%7 == 0,
		}
	}
	body, err := json.Marshal(entries)
	if err != nil {
		tb.Fatalf("json.Marshal() error = %v", err)
	}
	return body
}

func gzipBytes(tb testing.TB, body []byte) []byte {
	tb.Helper()

	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(body); err != nil {
		tb.Fatalf("gzip Write() error = %v", err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("gzip Close() error = %v", err)
	}
	return buf.Bytes()
}

// redirectTo sends the proxy's rewritten Riot requests to a test server.
func redirectTo(ts *httptest.Server) http.RoundTripper {
	base := ts.Client().Transport
	target, _ := url.Parse(ts.URL)
	return testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		r = r.Clone(r.Context())
		r.URL.Scheme = target.Scheme
		r.URL.Host = target.Host
		return base.RoundTrip(r)
	})
}

func writeJSON(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// writePartial sends headers and the first half of body, then lets the caller
// decide how the response fails.
func writePartial(w http.ResponseWriter, body []byte) {
	w.Header().Set("Content-Type", "application/json;charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body[:len(body)/2])
	w.(http.Flusher).Flush()
}

type relayHarness struct {
	handler   http.Handler
	collector *metrics.Collector
}

func newRelay(t *testing.T, cfg config.Config, upstream http.RoundTripper, l *limiter.Limiter) relayHarness {
	t.Helper()

	collector := metrics.NewCollector()
	opts := []Option{WithBaseTransport(upstream), WithMetrics(collector)}
	if l != nil {
		opts = append(opts, WithLimiter(l))
	}
	return relayHarness{handler: New(cfg, opts...), collector: collector}
}

func (h relayHarness) get(path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// counter returns the value of the first sample of name whose labels include
// all of labels.
func (h relayHarness) counter(t *testing.T, name string, labels map[string]string) float64 {
	t.Helper()

	rec := httptest.NewRecorder()
	h.collector.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	var total float64
	scanner := bufio.NewScanner(rec.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		matches := true
		for k, v := range labels {
			if !strings.Contains(line, k+`="`+v+`"`) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		value, err := strconv.ParseFloat(line[strings.LastIndexByte(line, ' ')+1:], 64)
		if err != nil {
			t.Fatalf("parse metric line %q: %v", line, err)
		}
		total += value
	}
	return total
}

func newLimiter(t *testing.T, keys int, appLimits string) *limiter.Limiter {
	t.Helper()

	l, err := limiter.New(limiter.Config{
		KeyCount:         keys,
		QueueCapacity:    8,
		DefaultAppLimits: appLimits,
	})
	if err != nil {
		t.Fatalf("limiter.New() error = %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l
}

func assertComplete(t *testing.T, rec *httptest.ResponseRecorder, want []byte) {
	t.Helper()

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", got, rec.Body.String())
	}
	if !bytes.Equal(rec.Body.Bytes(), want) {
		t.Fatalf("body = %d bytes, want the complete %d-byte page", rec.Body.Len(), len(want))
	}
	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(len(want)); got != want {
		t.Fatalf("Content-Length = %q, want %q", got, want)
	}
}

func assertFailure(t *testing.T, h relayHarness, cause, outcome string, want float64) {
	t.Helper()

	got := h.counter(t, "riftrelay_upstream_failures_total", map[string]string{
		"cause":    cause,
		"outcome":  outcome,
		"region":   "euw1",
		"endpoint": "lol/league/v4/entries/{queue}/{tier}/{division}",
	})
	if got != want {
		t.Fatalf("riftrelay_upstream_failures_total{cause=%q,outcome=%q} = %v, want %v", cause, outcome, got, want)
	}
}

// failingFirst serves fail for the first n requests and the full page afterwards.
func failingFirst(n int32, page []byte, fail func(http.ResponseWriter, *http.Request)) (http.HandlerFunc, *atomic.Int32) {
	var calls atomic.Int32
	return func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= n {
			fail(w, r)
			return
		}
		writeJSON(w, page)
	}, &calls
}

func TestUpstreamRetriesHTTP2StreamResetMidBody(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	handler, calls := failingFirst(1, page, func(w http.ResponseWriter, _ *http.Request) {
		writePartial(w, page)
		panic(http.ErrAbortHandler) // HTTP/2 server resets the stream
	})
	ts := httptest.NewUnstartedServer(handler)
	ts.EnableHTTP2 = true
	ts.Config.ErrorLog = nil
	ts.StartTLS()
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	assertComplete(t, rec, page)
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
	assertFailure(t, h, causeStreamReset, outcomeRetried, 1)
}

func TestUpstreamRetriesStalledBody(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	handler, calls := failingFirst(1, page, func(w http.ResponseWriter, r *http.Request) {
		writePartial(w, page)
		<-r.Context().Done()
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	cfg := testutil.DummyConfig()
	cfg.UpstreamTimeout = 300 * time.Millisecond
	h := newRelay(t, cfg, redirectTo(ts), newLimiter(t, 1, "100:1"))

	start := time.Now()
	rec := h.get(ladderPath)

	assertComplete(t, rec, page)
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("request took %v, want the stall cut off at the attempt deadline", elapsed)
	}
	assertFailure(t, h, causeBodyTimeout, outcomeRetried, 1)
}

func TestUpstreamRetriesUnexpectedEOF(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	handler, calls := failingFirst(1, page, func(w http.ResponseWriter, _ *http.Request) {
		writePartial(w, page)
		panic(http.ErrAbortHandler) // HTTP/1 server closes the connection
	})
	ts := httptest.NewServer(handler)
	ts.Config.ErrorLog = nil
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	assertComplete(t, rec, page)
	if got, want := calls.Load(), int32(2); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
	assertFailure(t, h, causeUnexpectedEOF, outcomeRetried, 1)
}

func TestUpstreamRetriesInvalidJSON(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	for _, tt := range []struct {
		name string
		fail func(http.ResponseWriter, *http.Request)
		want []byte
		ok   func(http.ResponseWriter)
	}{
		{
			name: "truncated JSON with complete framing",
			fail: func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, page[:len(page)/2])
			},
		},
		{
			name: "truncated gzip stream with complete framing",
			fail: func(w http.ResponseWriter, _ *http.Request) {
				zipped := gzipBytes(t, page)
				w.Header().Set("Content-Encoding", "gzip")
				writeJSON(w, zipped[:len(zipped)-8])
			},
		},
		{
			name: "valid gzip of invalid JSON",
			fail: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Encoding", "gzip")
				writeJSON(w, gzipBytes(t, page[:len(page)-1]))
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler, calls := failingFirst(1, page, tt.fail)
			ts := httptest.NewServer(handler)
			t.Cleanup(ts.Close)

			h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
			rec := h.get(ladderPath)

			assertComplete(t, rec, page)
			if got, want := calls.Load(), int32(2); got != want {
				t.Fatalf("upstream calls = %d, want %d", got, want)
			}
			assertFailure(t, h, causeInvalidBody, outcomeRetried, 1)
		})
	}
}

func TestUpstreamForwardsValidGzipUnchanged(t *testing.T) {
	t.Parallel()

	zipped := gzipBytes(t, ladderPage(t))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		writeJSON(w, zipped)
	}))
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	assertComplete(t, rec, zipped)
	if got, want := rec.Header().Get("Content-Encoding"), "gzip"; got != want {
		t.Fatalf("Content-Encoding = %q, want %q", got, want)
	}
}

func TestUpstreamRetriesGatewayTimeout(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	handler, calls := failingFirst(2, page, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "<html>504 Gateway Time-out</html>", http.StatusGatewayTimeout)
	})
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	assertComplete(t, rec, page)
	if got, want := calls.Load(), int32(3); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
	assertFailure(t, h, "http_504", outcomeRetried, 2)
}

func TestUpstreamReturnsLastGatewayTimeoutWhenRetriesExhausted(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "<html>504 Gateway Time-out</html>", http.StatusGatewayTimeout)
	}))
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get("/vn2/lol/league/v4/masterleagues/by-queue/RANKED_SOLO_5x5")

	if got, want := rec.Code, http.StatusGatewayTimeout; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := calls.Load(), int32(3); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
}

func TestUpstreamNeverForwardsTruncatedBody(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writePartial(w, page)
		panic(http.ErrAbortHandler)
	}))
	ts.Config.ErrorLog = nil
	t.Cleanup(ts.Close)

	h := newRelay(t, testutil.DummyConfig(), redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	if got, want := rec.Code, http.StatusBadGateway; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := rec.Header().Get("Retry-After"), "1"; got != want {
		t.Fatalf("Retry-After = %q, want %q", got, want)
	}
	if !strings.Contains(rec.Body.String(), causeUnexpectedEOF) {
		t.Fatalf("body = %q, want it to name the cause", rec.Body.String())
	}
	if got, want := calls.Load(), int32(3); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
	assertFailure(t, h, causeUnexpectedEOF, outcomeRetried, 2)
	assertFailure(t, h, causeUnexpectedEOF, outcomeExhausted, 1)
}

func TestUpstreamStalledBodyWithoutRetriesReturnsGatewayTimeout(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writePartial(w, page)
		<-r.Context().Done()
	}))
	t.Cleanup(ts.Close)

	cfg := testutil.DummyConfig()
	cfg.UpstreamTimeout = 200 * time.Millisecond
	cfg.UpstreamRetries = 0
	h := newRelay(t, cfg, redirectTo(ts), newLimiter(t, 1, "100:1"))
	rec := h.get(ladderPath)

	if got, want := rec.Code, http.StatusGatewayTimeout; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	assertFailure(t, h, causeBodyTimeout, outcomeExhausted, 1)
}

func TestUpstreamDoesNotRetryNonIdempotentRequests(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	h := newRelay(t, testutil.DummyConfig(), testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return testutil.HTTPResponse(http.StatusServiceUnavailable, "unavailable", nil), nil
	}), newLimiter(t, 1, "100:1"))

	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/americas/lol/tournament/v5/providers", strings.NewReader(`{}`)))

	if got, want := rec.Code, http.StatusServiceUnavailable; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := calls.Load(), int32(1); got != want {
		t.Fatalf("upstream calls = %d, want %d", got, want)
	}
}

func TestUpstreamStreamsBodiesLargerThanBuffer(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	collector := metrics.NewCollector()
	handler := New(testutil.DummyConfig(),
		WithMetrics(collector),
		WithBaseTransport(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			return testutil.HTTPResponseBytes(http.StatusOK, page, http.Header{"Content-Type": []string{"application/json"}}), nil
		})),
		func(o *options) { o.maxBody = 1024 },
	)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ladderPath, nil))

	if got := rec.Code; got != http.StatusOK {
		t.Fatalf("status = %d, want 200", got)
	}
	if !bytes.Equal(rec.Body.Bytes(), page) {
		t.Fatalf("body = %d bytes, want %d", rec.Body.Len(), len(page))
	}
}

// fakeBody returns data and then err, like a response cut off mid-body.
type fakeBody struct {
	r   io.Reader
	err error
}

func (b *fakeBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if errors.Is(err, io.EOF) {
		return n, b.err
	}
	return n, err
}

func (b *fakeBody) Close() error { return nil }

func cutResponse(err error) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       &fakeBody{r: strings.NewReader(`[{"leagueId":`), err: err},
	}
}

func TestFailureCause(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		err         string
		readingBody bool
		want        string
	}{
		{"stream error: stream ID 16357; INTERNAL_ERROR; received from peer", true, causeStreamReset},
		{`http2: server sent GOAWAY and closed the connection; LastStreamID=19999, ErrCode=NO_ERROR, debug=""`, true, causeGoAway},
		{"http2: timeout awaiting response headers", false, causeHeaderTimeout},
		{"net/http: timeout awaiting response headers", false, causeHeaderTimeout},
		{"dial tcp: connect: connection refused", false, causeConnection},
	} {
		if got := failureCause(errors.New(tt.err), tt.readingBody); got != tt.want {
			t.Errorf("failureCause(%q) = %q, want %q", tt.err, got, tt.want)
		}
	}

	if got := failureCause(fmt.Errorf("read: %w", io.ErrUnexpectedEOF), true); got != causeUnexpectedEOF {
		t.Errorf("failureCause(unexpected EOF) = %q, want %q", got, causeUnexpectedEOF)
	}
	if got := failureCause(context.DeadlineExceeded, true); got != causeBodyTimeout {
		t.Errorf("failureCause(deadline while reading body) = %q, want %q", got, causeBodyTimeout)
	}
	if got := failureCause(context.DeadlineExceeded, false); got != causeHeaderTimeout {
		t.Errorf("failureCause(deadline before headers) = %q, want %q", got, causeHeaderTimeout)
	}
}

func TestUpstreamRetriesGoAwayMidBody(t *testing.T) {
	t.Parallel()

	page := ladderPage(t)
	var calls atomic.Int32
	goAway := errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=19999, ErrCode=NO_ERROR, debug=""`)
	h := newRelay(t, testutil.DummyConfig(), testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return cutResponse(goAway), nil
		}
		return testutil.HTTPResponseBytes(http.StatusOK, page, http.Header{"Content-Type": []string{"application/json"}}), nil
	}), newLimiter(t, 1, "100:1"))

	rec := h.get(ladderPath)

	assertComplete(t, rec, page)
	assertFailure(t, h, causeGoAway, outcomeRetried, 1)
}

// tokenRecorder records the X-Riot-Token of each upstream attempt.
type tokenRecorder struct {
	mu     sync.Mutex
	tokens []string
}

func (r *tokenRecorder) add(token string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tokens = append(r.tokens, token)
	return len(r.tokens)
}

func (r *tokenRecorder) get() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.tokens...)
}

func TestUpstreamRetryIsAdmittedAndUsesAdmittedKey(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		page := ladderPage(t)
		var seen tokenRecorder
		l := newLimiter(t, 2, "1:60")
		h := newRelay(t, testutil.DummyConfig(), testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if seen.add(r.Header.Get("X-Riot-Token")) == 1 {
				return cutResponse(io.ErrUnexpectedEOF), nil
			}
			return testutil.HTTPResponseBytes(http.StatusOK, page, http.Header{"Content-Type": []string{"application/json"}}), nil
		}), l)

		rec := h.get(ladderPath)

		assertComplete(t, rec, page)
		// Each key allows one request per minute, so the retry can only go out
		// immediately on the other key.
		if got, want := seen.get(), []string{"test-token-a", "test-token-b"}; !equalStrings(got, want) {
			t.Fatalf("upstream tokens = %q, want %q", got, want)
		}
		if got := h.counter(t, "riftrelay_admission_total", map[string]string{"outcome": "allowed"}); got != 1 {
			t.Fatalf("riftrelay_admission_total{outcome=allowed} = %v, want 1 (retries are not client admissions)", got)
		}
	})
}

func TestUpstreamRetryWaitsForRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		page := ladderPage(t)
		var calls atomic.Int32
		l := newLimiter(t, 1, "1:2")
		cfg := testutil.DummyConfig()
		cfg.AdmissionTimeout = 3 * time.Second
		h := newRelay(t, cfg, testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			if calls.Add(1) <= 2 {
				return cutResponse(io.ErrUnexpectedEOF), nil
			}
			return testutil.HTTPResponseBytes(http.StatusOK, page, http.Header{"Content-Type": []string{"application/json"}}), nil
		}), l)

		start := time.Now()
		rec := h.get(ladderPath)

		// One request per 2s window: the two retries each wait for a new window.
		// Together they wait longer than ADMISSION_TIMEOUT, which only bounds
		// each admission, not the request.
		assertComplete(t, rec, page)
		if elapsed := time.Since(start); elapsed < 4*time.Second {
			t.Fatalf("request took %v, want retries held back by the 1:2 limit", elapsed)
		}
		if got := h.counter(t, "riftrelay_admission_total", map[string]string{"outcome": "rejected_timeout"}); got != 0 {
			t.Fatalf("riftrelay_admission_total{outcome=rejected_timeout} = %v, want 0", got)
		}
	})
}

func TestUpstreamRetryRejectedByAdmissionReturnsError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		l := newLimiter(t, 1, "1:60")
		cfg := testutil.DummyConfig()
		cfg.AdmissionTimeout = time.Second
		h := newRelay(t, cfg, testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return cutResponse(io.ErrUnexpectedEOF), nil
		}), l)

		rec := h.get(ladderPath)

		if got, want := rec.Code, http.StatusBadGateway; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := calls.Load(), int32(1); got != want {
			t.Fatalf("upstream calls = %d, want %d (retry must not bypass the limiter)", got, want)
		}
		assertFailure(t, h, causeUnexpectedEOF, outcomeRetryRejected, 1)
		if got := h.counter(t, "riftrelay_admission_total", map[string]string{"outcome": "rejected_timeout"}); got != 0 {
			t.Fatalf("riftrelay_admission_total{outcome=rejected_timeout} = %v, want 0", got)
		}
	})
}

func TestUpstreamRetryKeepsPinnedBearerRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		page := ladderPage(t)
		var seen tokenRecorder
		l, err := limiter.New(limiter.Config{
			KeyCount:          2,
			AutomaticKeyCount: 1,
			QueueCapacity:     2,
			DefaultAppLimits:  "100:1",
		})
		if err != nil {
			t.Fatalf("limiter.New() error = %v", err)
		}
		defer func() { _ = l.Close() }()

		cfg := testutil.DummyConfig()
		cfg.Tokens = []string{"automatic-token", "rso-rate-limit-slot"}
		cfg.AutomaticTokenCount = 1
		h := newRelay(t, cfg, testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("Authorization = %q, want the client's bearer token", r.Header.Get("Authorization"))
			}
			if seen.add(strings.Join(r.Header.Values("X-Riot-Token"), ",")) == 1 {
				return cutResponse(io.ErrUnexpectedEOF), nil
			}
			return testutil.HTTPResponseBytes(http.StatusOK, page, http.Header{"Content-Type": []string{"application/json"}}), nil
		}), l)

		req := httptest.NewRequest(http.MethodGet, "/europe/riot/account/v1/accounts/me", nil)
		req.Header.Set("Authorization", "Bearer access-token")
		req.Header.Set("X-Riot-Token-Index", "1")
		rec := httptest.NewRecorder()
		h.handler.ServeHTTP(rec, req)

		assertComplete(t, rec, page)
		if got, want := seen.get(), []string{"", ""}; !equalStrings(got, want) {
			t.Fatalf("upstream X-Riot-Token values = %q, want none on either attempt", got)
		}
	})
}

func TestUpstreamStopsRetryingWhenClientCancels(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		l := newLimiter(t, 1, "1:60")
		h := newRelay(t, testutil.DummyConfig(), testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			calls.Add(1)
			return cutResponse(io.ErrUnexpectedEOF), nil
		}), l)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			rec := httptest.NewRecorder()
			h.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ladderPath, nil).WithContext(ctx))
			done <- rec
		}()
		synctest.Wait() // the retry is queued behind the 1:60 limit
		cancel()

		rec := <-done
		if got, want := rec.Code, 499; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
		if got, want := calls.Load(), int32(1); got != want {
			t.Fatalf("upstream calls = %d, want %d", got, want)
		}
		assertFailure(t, h, causeUnexpectedEOF, outcomeCanceled, 1)
	})
}

func TestUpstreamRateLimitRetryWithoutLimiterHonorsCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		h := newRelay(t, testutil.DummyConfig(), testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
			return testutil.HTTPResponse(http.StatusTooManyRequests, "", http.Header{"Retry-After": []string{"10"}}), nil
		}), nil)

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan int, 1)
		go func() {
			rec := httptest.NewRecorder()
			h.handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, ladderPath, nil).WithContext(ctx))
			done <- rec.Code
		}()
		synctest.Wait()
		cancel()

		if got, want := <-done, 499; got != want {
			t.Fatalf("status = %d, want %d", got, want)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
