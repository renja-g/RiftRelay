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
	baseTransport http.RoundTripper
	limiter       *limiter.Limiter
	metrics       *metrics.Collector
	admitTimeout  time.Duration
	apiTokens     []string
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
		baseTransport: transport.New(),
		admitTimeout:  cfg.AdmissionTimeout,
		apiTokens:     cfg.Tokens,
	}
	for _, opt := range opts {
		opt(&o)
	}

	o.baseTransport = transport.WithRequestTimeout(o.baseTransport, cfg.UpstreamTimeout)
	o.baseTransport = transport.WithRetryAfter429(o.baseTransport, 3, func(req *http.Request, resp *http.Response) {
		observeResponse(o, req, resp, false)
	})

	rp := newReverseProxy(o)
	handler := http.Handler(rp)

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

			var statusCode int
			var msg string
			var retryAfter time.Duration
			switch {
			case errors.Is(err, context.Canceled):
				statusCode = 499
				msg = "client closed request"
			case errors.Is(err, context.DeadlineExceeded):
				statusCode = http.StatusRequestTimeout
				msg = "request timed out"
				retryAfter = time.Second
			default:
				statusCode = http.StatusBadGateway
				msg = "upstream unavailable"
			}

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
