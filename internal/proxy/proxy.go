package proxy

import (
	"log"
	"net/http"
	"net/http/httputil"
	"sync"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/ratelimit"
	"github.com/renja-g/RiftRelay/internal/router"
	"github.com/renja-g/RiftRelay/internal/scheduler"
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

// Middleware allows injecting middleware around the proxy handler.
type Middleware func(http.Handler) http.Handler

type options struct {
	baseTransport http.RoundTripper
	middlewares   []Middleware
}

type Option func(*options)

// WithBaseTransport overrides the HTTP transport used before retry wrapping.
func WithBaseTransport(rt http.RoundTripper) Option {
	return func(o *options) {
		o.baseTransport = rt
	}
}

// WithMiddleware adds handler middlewares.
func WithMiddleware(mw ...Middleware) Option {
	return func(o *options) {
		o.middlewares = append(o.middlewares, mw...)
	}
}

// New constructs the reverse proxy handler with optional middlewares.
func New(cfg config.Config, opts ...Option) http.Handler {
	o := options{
		baseTransport: defaultTransport(),
	}
	for _, opt := range opts {
		opt(&o)
	}

	rp := newReverseProxy(cfg, o)
	handler := router.ProxyHandler(rp)
	if len(o.middlewares) > 0 {
		handler = applyMiddleware(handler, o.middlewares...)
	}

	return handler
}

func newReverseProxy(cfg config.Config, o options) *httputil.ReverseProxy {
	sched := scheduler.NewRateScheduler(func() *ratelimit.State {
		return ratelimit.NewState(nil)
	})
	scheduledTransport := transport.NewScheduledTransport(o.baseTransport, sched)
	transportWithRetry := transport.NewRetryTransport(scheduledTransport, cfg.MaxRetries)

	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 32*1024) // 32KB
			return &buf
		},
	}

	director := func(req *http.Request) {
		info, ok := router.PathFromContext(req.Context())
		if !ok {
			if parsed, ok := router.ShiftPath(req.URL.Path); ok {
				info = parsed
			} else {
				return
			}
		}

		host := info.Region + ".api.riotgames.com"

		req.URL.Scheme = "https"
		req.URL.Host = host
		req.Host = host
		req.URL.Path = info.Path
		req.Header.Set("X-Riot-Token", cfg.Token)
	}

	return &httputil.ReverseProxy{
		Director:   director,
		Transport:  transportWithRetry,
		BufferPool: bufferPool{pool: pool},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error: %v", err)
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
		},
	}
}

func applyMiddleware(h http.Handler, middlewares ...Middleware) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}
	return h
}

func defaultTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          512,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}
