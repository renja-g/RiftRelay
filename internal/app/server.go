package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"

	"github.com/renja-g/RiftRelay/internal/config"
	"github.com/renja-g/RiftRelay/internal/limiter"
	"github.com/renja-g/RiftRelay/internal/metrics"
	"github.com/renja-g/RiftRelay/internal/proxy"
)

type Server struct {
	cfg     config.Config
	server  *http.Server
	limiter *limiter.Limiter
}

func New(cfg config.Config) (*Server, error) {
	var collector *metrics.Collector
	if cfg.MetricsEnabled {
		collector = metrics.NewCollector()
	}

	limiterCfg := limiter.Config{
		KeyCount:         len(cfg.Tokens),
		QueueCapacity:    cfg.QueueCapacity,
		AdditionalWindow: cfg.AdditionalWindow,
	}
	if collector != nil {
		limiterCfg.Metrics = collector
	}

	l, err := limiter.New(limiterCfg)
	if err != nil {
		return nil, fmt.Errorf("create limiter: %w", err)
	}

	proxyOptions := []proxy.Option{
		proxy.WithLimiter(l),
	}
	if collector != nil {
		proxyOptions = append(proxyOptions, proxy.WithMetrics(collector))
	}

	handler := proxy.New(cfg, proxyOptions...)
	if collector != nil {
		handler = collector.Middleware(handler)
	}

	mux := http.NewServeMux()
	mux.Handle("/", handler)
	if collector != nil {
		mux.Handle("/metrics", collector)
	}
	if cfg.PprofEnabled {
		mux.HandleFunc("/debug/pprof/", pprof.Index)
		mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
		mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
		mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout,
		ReadTimeout:       cfg.Server.ReadTimeout,
		WriteTimeout:      cfg.Server.WriteTimeout,
		IdleTimeout:       cfg.Server.IdleTimeout,
	}

	return &Server{
		cfg:     cfg,
		server:  srv,
		limiter: l,
	}, nil
}

func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)

	go func() {
		log.Printf("RiftRelay loaded %d API key(s)", len(s.cfg.Tokens))
		log.Printf("RiftRelay listening on http://localhost:%d", s.cfg.Port)
		if err := s.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		stopCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		return s.Shutdown(stopCtx)
	case err := <-errCh:
		if err != nil {
			return err
		}
		return nil
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	var errs []error
	if err := s.server.Shutdown(ctx); err != nil {
		errs = append(errs, err)
	}
	if err := s.limiter.Close(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}
