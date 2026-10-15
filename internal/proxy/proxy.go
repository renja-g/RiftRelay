package proxy

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/http/httputil"
	"strconv"
	"sync"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
	"github.com/renja-g/RiftRelay/internal/router"
	"github.com/renja-g/RiftRelay/internal/transport"
)

type bufferPool struct {
	pool *sync.Pool
}

func (p bufferPool) Get() []byte {
	return *(p.pool.Get().(*[]byte))
}

func (p bufferPool) Put(b []byte) {
	p.pool.Put(&b)
}

type options struct {
	baseTransport  http.RoundTripper
	limiter        *limiter.Limiter
	metrics        *metrics.Collector
	admitTimeout   time.Duration
	attemptTimeout time.Duration
	maxRetries     int
	maxBody        int
	apiTokens      []string
}

type Option func(*options)

func WithBaseTransport(rt http.RoundTripper) Option {
	return func(o *options) {
		if rt != nil {
			o.baseTransport = rt
		}
	}
}

func WithLimiter(l *limiter.Limiter) Option {
	return func(o *options) {
		o.limiter = l
	}
}

func WithMetrics(m *metrics.Collector) Option {
	return func(o *options) {
		o.metrics = m
	}
}

// New constructs the reverse proxy handler.
func New(cfg config.Config, opts ...Option) http.Handler {
	o := options{
		baseTransport:  transport.New(),
		admitTimeout:   cfg.AdmissionTimeout,
		attemptTimeout: cfg.UpstreamTimeout,
		maxRetries:     cfg.UpstreamRetries,
		maxBody:        defaultMaxBufferedBody,
		apiTokens:      cfg.Tokens,
	}
	for _, opt := range opts {
		opt(&o)
	}

	o.baseTransport = &upstreamTransport{
		base:           o.baseTransport,
		limiter:        o.limiter,
		metrics:        o.metrics,
		tokens:         o.apiTokens,
		attemptTimeout: o.attemptTimeout,
		admitTimeout:   o.admitTimeout,
		maxRetries:     o.maxRetries,
		maxBody:        o.maxBody,
		observe: func(req *http.Request, resp *http.Response) {
			observeResponse(o, req, resp, false)
		},
	}

	rp := newReverseProxy(o)
	handler := http.Handler(rp)
	if cfg.Server.WriteTimeout > 0 {
		handler = withResponseController(handler)
	}

	if o.limiter != nil {
		automaticTokenCount := cfg.AutomaticTokenCount
		if automaticTokenCount == 0 {
			automaticTokenCount = len(cfg.Tokens)
		}
		handler = admissionMiddleware(o.limiter, o.metrics, o.admitTimeout, automaticTokenCount)(handler)
	}
	if o.metrics != nil {
		handler = o.metrics.Middleware(handler)
	}
	handler = router.ProxyHandler(handler) // Outermost — parse path first

	return handler
}

type responseControllerKey struct{}

// withResponseController lets the upstream transport extend the write deadline
// when it retries.
func withResponseController(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), responseControllerKey{}, http.NewResponseController(w))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newReverseProxy(o options) *httputil.ReverseProxy {
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 32*1024) // 32KB
			return &buf
		},
	}

	rewrite := func(preq *httputil.ProxyRequest) {
		info, ok := router.PathFromContext(preq.In.Context())
		if !ok {
			return
		}

		host := info.Region + ".api.riotgames.com"

		preq.Out.URL.Scheme = "https"
		preq.Out.URL.Host = host
		preq.Out.Host = host
		preq.Out.URL.Path = info.UpstreamPath

		admission, admitted := admissionFromContext(preq.In.Context())
		keyIndex := admission.KeyIndex
		if !admitted || keyIndex < 0 || keyIndex >= len(o.apiTokens) {
			keyIndex = 0
		}
		if admitted && admission.AuthMode == authModeBearer {
			preq.Out.Header.Del("X-Riot-Token")
		} else if len(o.apiTokens) > 0 {
			preq.Out.Header.Set("X-Riot-Token", o.apiTokens[keyIndex])
		}
		preq.Out.Header.Set("Accept-Encoding", "gzip")
		preq.SetXForwarded()
	}

	return &httputil.ReverseProxy{
		Rewrite:    rewrite,
		Transport:  o.baseTransport,
		BufferPool: bufferPool{pool: pool},
		ModifyResponse: func(resp *http.Response) error {
			observeResponse(o, resp.Request, resp, true)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %v", err)
			prio := "normal"
			region := "unknown"
			bucket := "unknown"

			statusCode, msg, retryAfter := errorResponse(err)

			if info, ok := admissionFromContext(r.Context()); ok && o.limiter != nil {
				prio = info.Priority
				region = info.Region
				bucket = info.Bucket
				o.limiter.Observe(limiter.Observation{
					Region:     info.Region,
					Bucket:     info.Bucket,
					KeyIndex:   info.KeyIndex,
					StatusCode: statusCode,
					Header:     http.Header{},
				})
			}
			if o.metrics != nil {
				o.metrics.ObserveUpstream(statusCode, region, bucket, prio)
			}
			if retryAfter > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Round(time.Second).Seconds())))
			}
			http.Error(w, msg, statusCode)
		},
	}
}

// errorResponse maps a proxy error to the status sent to the client. Upstream
// failures get a 5xx with Retry-After, which clients treat as retryable.
func errorResponse(err error) (int, string, time.Duration) {
	var upstreamErr *upstreamError
	switch {
	case errors.Is(err, context.Canceled):
		return 499, "client closed request", 0
	case errors.As(err, &upstreamErr) && upstreamErr.timeout():
		return http.StatusGatewayTimeout, upstreamFailureMessage(upstreamErr), time.Second
	case errors.As(err, &upstreamErr):
		return http.StatusBadGateway, upstreamFailureMessage(upstreamErr), time.Second
	case errors.Is(err, context.DeadlineExceeded):
		return http.StatusGatewayTimeout, "upstream timed out", time.Second
	default:
		return http.StatusBadGateway, "upstream unavailable", 0
	}
}

func upstreamFailureMessage(err *upstreamError) string {
	return "upstream request failed (" + err.cause + ") after " + strconv.Itoa(err.attempts) + " attempt(s)"
}

func observeResponse(o options, req *http.Request, resp *http.Response, recordDuration bool) {
	if req == nil || resp == nil {
		return
	}
	info, ok := admissionFromContext(req.Context())
	if !ok {
		return
	}

	if o.limiter != nil {
		o.limiter.Observe(limiter.Observation{
			Region:     info.Region,
			Bucket:     info.Bucket,
			KeyIndex:   info.KeyIndex,
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
		})
	}
	if o.metrics == nil {
		return
	}

	o.metrics.ObserveUpstream(resp.StatusCode, info.Region, info.Bucket, info.Priority)
	if recordDuration {
		o.metrics.ObserveUpstreamDuration(info.Region, info.Bucket, time.Since(info.StartedAt))
	}
}
