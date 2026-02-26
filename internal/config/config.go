package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultPort                   = 8985
	defaultQueueCapacity          = 2048
	defaultAdmissionTimeout       = 5 * time.Minute
	defaultAdditionalWindowSize   = 10 * time.Millisecond
	defaultShutdownTimeout        = 20 * time.Second
	defaultReadHeaderTimeout      = 10 * time.Second
	defaultReadTimeout            = 10 * time.Second
	defaultWriteTimeout           = 5 * time.Minute
	defaultIdleTimeout            = 90 * time.Second
	defaultMaxIdleConns           = 512
	defaultMaxIdleConnsPerHost    = 256
	defaultMaxConnsPerHost        = 0
	defaultIdleConnTimeout        = 90 * time.Second
	defaultTLSHandshakeTimeout    = 10 * time.Second
	defaultExpectContinueTimeout  = 1 * time.Second
	defaultDialTimeout            = 5 * time.Second
	defaultDialKeepAlive          = 30 * time.Second
	defaultResponseHeaderTimeout  = 15 * time.Second
	defaultForceAttemptHTTP2      = true
	defaultEnableMetrics          = true
	defaultEnablePprof            = false
	defaultEnableSwagger          = true
	defaultUpstreamRequestTimeout = 0
)

type Config struct {
	Tokens            []string
	Port              int
	QueueCapacity     int
	AdmissionTimeout  time.Duration
	AdditionalWindow  time.Duration
	ShutdownTimeout   time.Duration
	MetricsEnabled    bool
	PprofEnabled      bool
	SwaggerEnabled    bool
	UpstreamTimeout   time.Duration
	Server            ServerConfig
	UpstreamTransport UpstreamTransportConfig
}

type ServerConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
}

type UpstreamTransportConfig struct {
	MaxIdleConns          int
	MaxIdleConnsPerHost   int
	MaxConnsPerHost       int
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ExpectContinueTimeout time.Duration
	DialTimeout           time.Duration
	DialKeepAlive         time.Duration
	ResponseHeaderTimeout time.Duration
	ForceAttemptHTTP2     bool
}

func Load() (Config, error) {
	var errs []error

	cfg := Config{
		Port:             defaultPort,
		QueueCapacity:    defaultQueueCapacity,
		AdmissionTimeout: defaultAdmissionTimeout,
		AdditionalWindow: defaultAdditionalWindowSize,
		ShutdownTimeout:  defaultShutdownTimeout,
		MetricsEnabled:   defaultEnableMetrics,
		PprofEnabled:     defaultEnablePprof,
		SwaggerEnabled:   defaultEnableSwagger,
		UpstreamTimeout:  defaultUpstreamRequestTimeout,
		Server: ServerConfig{
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			ReadTimeout:       defaultReadTimeout,
			WriteTimeout:      defaultWriteTimeout,
			IdleTimeout:       defaultIdleTimeout,
		},
		UpstreamTransport: UpstreamTransportConfig{
			MaxIdleConns:          defaultMaxIdleConns,
			MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
			MaxConnsPerHost:       defaultMaxConnsPerHost,
			IdleConnTimeout:       defaultIdleConnTimeout,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ExpectContinueTimeout: defaultExpectContinueTimeout,
			DialTimeout:           defaultDialTimeout,
			DialKeepAlive:         defaultDialKeepAlive,
			ResponseHeaderTimeout: defaultResponseHeaderTimeout,
			ForceAttemptHTTP2:     defaultForceAttemptHTTP2,
		},
	}

	tokens := splitCSVEnv("RIOT_TOKEN")
	if len(tokens) == 0 {
		errs = append(errs, fmt.Errorf("RIOT_TOKEN env var is required"))
	} else {
		cfg.Tokens = tokens
	}

	mustParseInt("PORT", &cfg.Port, 1, &errs)
	mustParseInt("QUEUE_CAPACITY", &cfg.QueueCapacity, 1, &errs)
	mustParseDuration("ADMISSION_TIMEOUT", &cfg.AdmissionTimeout, &errs)
	mustParseDuration("ADDITIONAL_WINDOW_SIZE", &cfg.AdditionalWindow, &errs)
	mustParseDuration("SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout, &errs)
	mustParseDuration("UPSTREAM_TIMEOUT", &cfg.UpstreamTimeout, &errs)

	mustParseBool("ENABLE_METRICS", &cfg.MetricsEnabled, &errs)
	mustParseBool("ENABLE_PPROF", &cfg.PprofEnabled, &errs)
	mustParseBool("ENABLE_SWAGGER", &cfg.SwaggerEnabled, &errs)

	mustParseDuration("SERVER_READ_HEADER_TIMEOUT", &cfg.Server.ReadHeaderTimeout, &errs)
	mustParseDuration("SERVER_READ_TIMEOUT", &cfg.Server.ReadTimeout, &errs)
	mustParseDuration("SERVER_WRITE_TIMEOUT", &cfg.Server.WriteTimeout, &errs)
	mustParseDuration("SERVER_IDLE_TIMEOUT", &cfg.Server.IdleTimeout, &errs)

	mustParseInt("TRANSPORT_MAX_IDLE_CONNS", &cfg.UpstreamTransport.MaxIdleConns, 1, &errs)
	mustParseInt("TRANSPORT_MAX_IDLE_CONNS_PER_HOST", &cfg.UpstreamTransport.MaxIdleConnsPerHost, 1, &errs)
	mustParseInt("TRANSPORT_MAX_CONNS_PER_HOST", &cfg.UpstreamTransport.MaxConnsPerHost, 0, &errs)
	mustParseDuration("TRANSPORT_IDLE_CONN_TIMEOUT", &cfg.UpstreamTransport.IdleConnTimeout, &errs)
	mustParseDuration("TRANSPORT_TLS_HANDSHAKE_TIMEOUT", &cfg.UpstreamTransport.TLSHandshakeTimeout, &errs)
	mustParseDuration("TRANSPORT_EXPECT_CONTINUE_TIMEOUT", &cfg.UpstreamTransport.ExpectContinueTimeout, &errs)
	mustParseDuration("TRANSPORT_DIAL_TIMEOUT", &cfg.UpstreamTransport.DialTimeout, &errs)
	mustParseDuration("TRANSPORT_DIAL_KEEP_ALIVE", &cfg.UpstreamTransport.DialKeepAlive, &errs)
	mustParseDuration("TRANSPORT_RESPONSE_HEADER_TIMEOUT", &cfg.UpstreamTransport.ResponseHeaderTimeout, &errs)
	mustParseBool("TRANSPORT_FORCE_HTTP2", &cfg.UpstreamTransport.ForceAttemptHTTP2, &errs)

	if cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be <= 65535"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	return cfg, nil
}

func splitCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func mustParseInt(key string, dst *int, min int, errs *[]error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be an integer: %w", key, err))
		return
	}
	if parsed < min {
		*errs = append(*errs, fmt.Errorf("%s must be >= %d", key, min))
		return
	}
	*dst = parsed
}

func mustParseDuration(key string, dst *time.Duration, errs *[]error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}

	parsed, err := time.ParseDuration(value)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a valid duration (example: 150ms, 2s): %w", key, err))
		return
	}
	if parsed < 0 {
		*errs = append(*errs, fmt.Errorf("%s must be >= 0", key))
		return
	}
	*dst = parsed
}

func mustParseBool(key string, dst *bool, errs *[]error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s must be a boolean: %w", key, err))
		return
	}
	*dst = parsed
}
