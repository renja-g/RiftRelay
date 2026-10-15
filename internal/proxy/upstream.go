package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/renja-g/RiftRelay/internal/httputil"
	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
)

const (
	maxRateLimitRetries = 3

	// defaultMaxBufferedBody bounds how much of a response is held before headers
	// are sent. Larger bodies are streamed and can no longer be retried.
	defaultMaxBufferedBody = 16 << 20
	maxValidatedJSON       = 64 << 20
	maxPooledBuffer        = 1 << 20

	retryBaseDelay       = 100 * time.Millisecond
	statusRetryBaseDelay = 500 * time.Millisecond
	// A 5xx asking to wait longer than this is returned to the client instead.
	maxStatusRetryAfter = 5 * time.Second

	writeDeadlineSlack = 30 * time.Second
	unboundedBudget    = 5 * time.Minute
)

// Failure causes, the cause label of riftrelay_upstream_failures_total.
const (
	causeStreamReset   = "stream_reset"
	causeGoAway        = "goaway"
	causeUnexpectedEOF = "unexpected_eof"
	causeConnection    = "connection"
	causeHeaderTimeout = "header_timeout"
	causeBodyTimeout   = "body_timeout"
	causeInvalidBody   = "invalid_body"
	causeRateLimited   = "http_429"
)

// Outcomes, the outcome label of riftrelay_upstream_failures_total.
const (
	outcomeRetried       = "retried"
	outcomeExhausted     = "exhausted"
	outcomeNotRetryable  = "not_retryable"
	outcomeRetryRejected = "retry_rejected"
	outcomeCanceled      = "canceled"
)

// upstreamTransport sends a request to Riot until it has a complete response to
// forward. GET and HEAD bodies are read and checked before anything reaches the
// client, so a transient failure can be retried and an unrecoverable one turns
// into an error status rather than a truncated 200. Every retry is admitted by
// the limiter again.
type upstreamTransport struct {
	base           http.RoundTripper
	limiter        *limiter.Limiter
	metrics        *metrics.Collector
	tokens         []string
	attemptTimeout time.Duration
	admitTimeout   time.Duration
	maxRetries     int
	maxBody        int
	observe        func(*http.Request, *http.Response)
}

// upstreamError is returned when RiftRelay gives up without a complete
// upstream response to forward.
type upstreamError struct {
	cause    string
	attempts int
	err      error
}

func (e *upstreamError) Error() string {
	return fmt.Sprintf("upstream %s after %d attempt(s): %v", e.cause, e.attempts, e.err)
}

func (e *upstreamError) Unwrap() error {
	return e.err
}

func (e *upstreamError) timeout() bool {
	return e.cause == causeHeaderTimeout || e.cause == causeBodyTimeout
}

type invalidBodyError struct {
	reason string
}

func (e *invalidBodyError) Error() string {
	return "invalid response body: " + e.reason
}

// attempt is the result of one upstream request.
type attempt struct {
	// resp is a response that can be forwarded as is.
	resp *http.Response
	// header is the failed response whose body could not be read, if any.
	header *http.Response
	err    error
	// cause is empty when the attempt succeeded.
	cause         string
	retryAfter    time.Duration
	hasRetryAfter bool
}

func (a attempt) received() *http.Response {
	if a.resp != nil {
		return a.resp
	}
	return a.header
}

func (a attempt) discard() {
	if a.resp != nil && a.resp.Body != nil {
		_, _ = io.Copy(io.Discard, a.resp.Body)
		_ = a.resp.Body.Close()
	}
}

func (t *upstreamTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	idempotent := req.Method == http.MethodGet || req.Method == http.MethodHead
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil

	var failures, rateLimited int
	for attempts := 1; ; attempts++ {
		a := t.roundTrip(req, idempotent)
		if a.cause == "" {
			return a.resp, nil
		}
		if err := req.Context().Err(); err != nil {
			t.recordFailure(req, a, outcomeCanceled, attempts)
			t.observeReceived(req, a)
			a.discard()
			return nil, err
		}

		wait, outcome := t.retryPlan(a, idempotent, replayable, failures, rateLimited)
		if outcome != outcomeRetried {
			t.recordFailure(req, a, outcome, attempts)
			return t.giveUp(req, a, attempts)
		}

		// The limiter must see this attempt's rate-limit headers (and any 429)
		// before the retry is admitted.
		t.observeReceived(req, a)

		next, err := t.prepareRetry(req, wait)
		if err != nil {
			return t.retryFailed(req, a, attempts, err)
		}
		t.recordFailure(req, a, outcomeRetried, attempts)
		a.discard()

		if a.cause == causeRateLimited {
			rateLimited++
		} else {
			failures++
		}
		req = next
	}
}

// roundTrip performs one attempt under the per-attempt deadline. When buffer is
// set, the deadline covers reading the whole body.
func (t *upstreamTransport) roundTrip(req *http.Request, buffer bool) attempt {
	ctx, cancel := req.Context(), context.CancelFunc(func() {})
	if t.attemptTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, t.attemptTimeout)
		req = req.WithContext(ctx)
	}

	resp, err := t.base.RoundTrip(req)
	if err != nil {
		cancel()
		return attempt{err: err, cause: failureCause(err, false)}
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	if !buffer {
		if a := statusAttempt(resp); a.cause == "" {
			resp.Body = &cancelOnCloseBody{ReadCloser: resp.Body, cancel: cancel}
			return a
		}
		// Error bodies are small; holding them lets the response be forwarded
		// if the retry is not admitted.
	}

	if err := t.bufferBody(req.Method, resp, cancel); err != nil {
		return attempt{err: err, cause: failureCause(err, true), header: resp}
	}
	return statusAttempt(resp)
}

func statusAttempt(resp *http.Response) attempt {
	a := attempt{resp: resp}
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		a.cause = causeRateLimited
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		a.cause = "http_" + strconv.Itoa(resp.StatusCode)
	default:
		return a
	}
	a.retryAfter, a.hasRetryAfter = httputil.ParseRetryAfter(resp.Header.Get("Retry-After"))
	return a
}

// bufferBody reads the whole response body and replaces it with the buffered
// copy. On error, the response body is closed and must not be used.
func (t *upstreamTransport) bufferBody(method string, resp *http.Response, cancel context.CancelFunc) error {
	buf := getBuffer()
	if resp.ContentLength > 0 && resp.ContentLength <= int64(t.maxBody) {
		buf.Grow(int(resp.ContentLength))
	}
	_, err := buf.ReadFrom(io.LimitReader(resp.Body, int64(t.maxBody)+1))
	if err != nil {
		_ = resp.Body.Close()
		cancel()
		putBuffer(buf)
		return err
	}

	if buf.Len() > t.maxBody {
		// Too large to hold: stream the rest. A failure from here on reaches the
		// client as a truncated body, as it did before buffering.
		resp.Body = &bufferedBody{buf: buf, rest: resp.Body, cancel: cancel}
		return nil
	}

	_ = resp.Body.Close()
	cancel()
	if err := validateBody(method, resp, buf.Bytes()); err != nil {
		putBuffer(buf)
		return err
	}

	resp.Body = &bufferedBody{buf: buf}
	if method != http.MethodHead {
		// The client gets an exact length, so a cut between RiftRelay and the
		// client is detectable too.
		resp.ContentLength = int64(buf.Len())
		resp.TransferEncoding = nil
		resp.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
	}
	return nil
}

// retryPlan decides whether a failed attempt is retried and how long to wait
// before admitting the retry.
func (t *upstreamTransport) retryPlan(a attempt, idempotent, replayable bool, failures, rateLimited int) (time.Duration, string) {
	if a.cause == causeRateLimited {
		switch {
		case !replayable || !a.hasRetryAfter:
			return 0, outcomeNotRetryable
		case rateLimited >= maxRateLimitRetries:
			return 0, outcomeExhausted
		case t.limiter != nil:
			// The limiter holds the retry back until Retry-After has passed.
			return 0, outcomeRetried
		default:
			return a.retryAfter, outcomeRetried
		}
	}

	switch {
	case !idempotent || !replayable:
		return 0, outcomeNotRetryable
	case failures >= t.maxRetries:
		return 0, outcomeExhausted
	case a.resp == nil:
		return backoff(retryBaseDelay, failures), outcomeRetried
	case !a.hasRetryAfter:
		return backoff(statusRetryBaseDelay, failures), outcomeRetried
	case a.retryAfter > maxStatusRetryAfter:
		return 0, outcomeNotRetryable
	default:
		return a.retryAfter, outcomeRetried
	}
}

// backoff returns base*2^n with jitter in [d/2, d).
func backoff(base time.Duration, n int) time.Duration {
	d := base << min(n, 6)
	return d/2 + rand.N(d/2)
}

// prepareRetry waits, admits the retry through the limiter, and returns the
// request to send. The retry keeps the client's region, bucket, budget and
// pinned token, and uses high priority: it skips pacing because it already
// waited its turn, but still respects Riot's limits and any Retry-After.
func (t *upstreamTransport) prepareRetry(req *http.Request, wait time.Duration) (*http.Request, error) {
	ctx := req.Context()
	if err := sleep(ctx, wait); err != nil {
		return nil, err
	}

	t.extendWriteDeadline(ctx)

	info, admitted := admissionFromContext(ctx)
	if admitted && t.limiter != nil {
		// A fresh timeout: the retry must not inherit the original queue wait.
		admitCtx, cancel := ctx, context.CancelFunc(func() {})
		if t.admitTimeout > 0 {
			admitCtx, cancel = context.WithTimeout(ctx, t.admitTimeout)
		}
		ticket, err := t.limiter.Admit(admitCtx, limiter.Admission{
			Region:     info.Region,
			Bucket:     info.Bucket,
			BudgetID:   info.BudgetID,
			Priority:   limiter.PriorityHigh,
			TokenIndex: info.TokenIndex,
		})
		cancel()
		if err != nil {
			return nil, err
		}
		info.KeyIndex = ticket.KeyIndex
		info.StartedAt = time.Now()
		ctx = withAdmission(ctx, info)
	}

	next := req.Clone(ctx)
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, fmt.Errorf("clone request body: %w", err)
		}
		next.Body = body
	}
	if admitted && info.AuthMode != authModeBearer && info.KeyIndex >= 0 && info.KeyIndex < len(t.tokens) {
		next.Header.Set("X-Riot-Token", t.tokens[info.KeyIndex])
	}
	return next, nil
}

// extendWriteDeadline gives the server connection time for the retry's
// admission and attempt, which the server's WriteTimeout does not include.
func (t *upstreamTransport) extendWriteDeadline(ctx context.Context) {
	rc, ok := ctx.Value(responseControllerKey{}).(*http.ResponseController)
	if !ok {
		return
	}
	admit, attemptBudget := t.admitTimeout, t.attemptTimeout
	if admit <= 0 {
		admit = unboundedBudget
	}
	if attemptBudget <= 0 {
		attemptBudget = unboundedBudget
	}
	_ = rc.SetWriteDeadline(time.Now().Add(admit + attemptBudget + writeDeadlineSlack))
}

// retryFailed handles a retry that was not admitted or whose client left while
// it waited. The failed attempt's response, if complete, is forwarded.
func (t *upstreamTransport) retryFailed(req *http.Request, a attempt, attempts int, err error) (*http.Response, error) {
	if ctxErr := req.Context().Err(); ctxErr != nil {
		t.recordFailure(req, a, outcomeCanceled, attempts)
		a.discard()
		return nil, ctxErr
	}
	t.recordFailure(req, a, outcomeRetryRejected, attempts)
	log.Printf("upstream_retry_rejected cause=%s attempt=%d err=%v", a.cause, attempts, err)
	if a.resp != nil {
		return a.resp, nil
	}
	return nil, t.failureError(a, attempts)
}

func (t *upstreamTransport) giveUp(req *http.Request, a attempt, attempts int) (*http.Response, error) {
	if a.resp != nil {
		return a.resp, nil
	}
	t.observeReceived(req, a)
	return nil, t.failureError(a, attempts)
}

// observeReceived passes the headers of a response that will not be forwarded
// to the limiter and metrics. Forwarded responses are observed by the proxy.
func (t *upstreamTransport) observeReceived(req *http.Request, a attempt) {
	if received := a.received(); received != nil && t.observe != nil {
		t.observe(req, received)
	}
}

func (t *upstreamTransport) failureError(a attempt, attempts int) error {
	err := a.err
	if err == nil {
		err = errors.New(a.received().Status)
	}
	return &upstreamError{cause: a.cause, attempts: attempts, err: err}
}

func (t *upstreamTransport) recordFailure(req *http.Request, a attempt, outcome string, attempts int) {
	region, bucket := "unknown", "unknown"
	if info, ok := admissionFromContext(req.Context()); ok {
		region, bucket = info.Region, info.Bucket
	}
	if t.metrics != nil {
		t.metrics.ObserveUpstreamFailure(a.cause, outcome, region, bucket)
	}
	if a.err != nil {
		// Status failures are visible in metrics; transport and body failures
		// are rare enough to log each one.
		log.Printf("upstream_failure region=%s bucket=%s cause=%s outcome=%s attempt=%d err=%v",
			region, bucket, a.cause, outcome, attempts, a.err)
	}
}

// failureCause classifies a transport or body read error. Go's HTTP/2 errors
// are unexported, so stream resets and GOAWAYs are matched by message.
func failureCause(err error, readingBody bool) string {
	var invalid *invalidBodyError
	if errors.As(err, &invalid) {
		return causeInvalidBody
	}

	msg := err.Error()
	var netErr net.Error
	switch {
	case strings.Contains(msg, "timeout awaiting response headers"):
		return causeHeaderTimeout
	case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
		if readingBody {
			return causeBodyTimeout
		}
		return causeHeaderTimeout
	case strings.Contains(msg, "GOAWAY"):
		return causeGoAway
	case strings.Contains(msg, "stream error"):
		return causeStreamReset
	case errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF):
		return causeUnexpectedEOF
	default:
		return causeConnection
	}
}

// validateBody checks that a successful JSON response is complete JSON,
// decompressing it first if needed.
func validateBody(method string, resp *http.Response, body []byte) error {
	if method == http.MethodHead || resp.StatusCode < 200 || resp.StatusCode >= 300 || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if !isJSONContentType(resp.Header.Get("Content-Type")) {
		return nil
	}

	switch encoding := strings.TrimSpace(resp.Header.Get("Content-Encoding")); {
	case encoding == "" || strings.EqualFold(encoding, "identity"):
		return checkJSON(body)
	case strings.EqualFold(encoding, "gzip"):
		decoded := getBuffer()
		defer putBuffer(decoded)
		complete, err := gunzip(decoded, body)
		if err != nil {
			return &invalidBodyError{reason: "gzip: " + err.Error()}
		}
		if !complete {
			return nil
		}
		return checkJSON(decoded.Bytes())
	default:
		return nil
	}
}

func checkJSON(body []byte) error {
	if !json.Valid(body) {
		return &invalidBodyError{reason: "not valid JSON (" + strconv.Itoa(len(body)) + " bytes)"}
	}
	return nil
}

func isJSONContentType(contentType string) bool {
	mediaType, _, _ := strings.Cut(contentType, ";")
	mediaType = strings.TrimSpace(mediaType)
	return strings.EqualFold(mediaType, "application/json") ||
		len(mediaType) > 5 && strings.EqualFold(mediaType[len(mediaType)-5:], "+json")
}

var gzipReaders sync.Pool

// gunzip decompresses src into dst, checking the gzip checksum. It reports
// false when the output exceeds maxValidatedJSON and was not fully decoded.
func gunzip(dst *bytes.Buffer, src []byte) (bool, error) {
	br := bytes.NewReader(src)
	zr, _ := gzipReaders.Get().(*gzip.Reader)
	if zr == nil {
		var err error
		if zr, err = gzip.NewReader(br); err != nil {
			return false, err
		}
	} else if err := zr.Reset(br); err != nil {
		gzipReaders.Put(zr)
		return false, err
	}
	defer gzipReaders.Put(zr)

	n, err := dst.ReadFrom(io.LimitReader(zr, maxValidatedJSON+1))
	if err != nil {
		return false, err
	}
	return n <= maxValidatedJSON, nil
}

var buffers = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func getBuffer() *bytes.Buffer {
	return buffers.Get().(*bytes.Buffer)
}

func putBuffer(buf *bytes.Buffer) {
	if buf.Cap() > maxPooledBuffer {
		return
	}
	buf.Reset()
	buffers.Put(buf)
}

// bufferedBody serves a buffered response body, followed by the unread rest of
// the upstream body when the response was too large to buffer.
type bufferedBody struct {
	buf    *bytes.Buffer
	rest   io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
}

func (b *bufferedBody) Read(p []byte) (int, error) {
	if b.buf != nil && b.buf.Len() > 0 {
		return b.buf.Read(p)
	}
	if b.rest != nil {
		return b.rest.Read(p)
	}
	return 0, io.EOF
}

func (b *bufferedBody) Close() error {
	var err error
	b.once.Do(func() {
		if b.rest != nil {
			err = b.rest.Close()
		}
		if b.cancel != nil {
			b.cancel()
		}
		putBuffer(b.buf)
		b.buf = nil
	})
	return err
}

type cancelOnCloseBody struct {
	io.ReadCloser
	cancel     context.CancelFunc
	cancelOnce sync.Once
}

func (b *cancelOnCloseBody) Close() error {
	b.cancelOnce.Do(b.cancel)
	return b.ReadCloser.Close()
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
