package proxy

import (
	"context"
	"log"
	"net/http"
	"net/http/httputil"
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
		baseTransport: transport.New(cfg.UpstreamTransport),
		admitTimeout:  cfg.AdmissionTimeout,
		apiTokens:     cfg.Tokens,
	}
	for _, opt := range opts {
		opt(&o)
	}

	o.baseTransport = transport.WithRequestTimeout(o.baseTransport, cfg.UpstreamTimeout)

	rp := newReverseProxy(o)
	handler := http.Handler(rp)

	if o.limiter != nil {
		handler = admissionMiddleware(o.limiter, o.metrics, o.admitTimeout)(handler)
	}

	handler = router.ProxyHandler(handler)

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
			if parsed, err := router.ParsePath(preq.In.URL.Path); err == nil {
				info = parsed
			} else {
				return
			}
		}

		host := info.Region + ".api.riotgames.com"

		preq.Out.URL.Scheme = "https"
		preq.Out.URL.Host = host
		preq.Out.Host = host
		preq.Out.URL.Path = info.UpstreamPath

		keyIndex := 0
		if value, ok := keyIndexFromContext(preq.In.Context()); ok && value >= 0 && value < len(o.apiTokens) {
			keyIndex = value
		}
		if len(o.apiTokens) > 0 {
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
			if o.limiter == nil {
				return nil
			}

			info, ok := admissionFromContext(resp.Request.Context())
			if !ok {
				return nil
			}

			o.limiter.Observe(limiter.Observation{
				Region:     info.Region,
				Bucket:     info.Bucket,
				KeyIndex:   info.KeyIndex,
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
			})

			if o.metrics != nil {
				o.metrics.ObserveUpstream(resp.StatusCode, time.Since(info.StartedAt))
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %v", err)
			if o.metrics != nil {
				o.metrics.ObserveUpstream(http.StatusBadGateway, 0)
			}
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
}

type keyIndexContextKey struct{}

func withKeyIndex(ctx context.Context, keyIndex int) context.Context {
	return context.WithValue(ctx, keyIndexContextKey{}, keyIndex)
}

func keyIndexFromContext(ctx context.Context) (int, bool) {
	value, ok := ctx.Value(keyIndexContextKey{}).(int)
	return value, ok
}
