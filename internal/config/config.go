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
	// User-facing defaults (env-configurable)
	defaultPort                 = 8985
	defaultQueueCapacity        = 2048
	defaultAdmissionTimeout     = 5 * time.Minute
	defaultAdditionalWindowSize = 150 * time.Millisecond
	defaultShutdownTimeout      = 20 * time.Second
	defaultEnableMetrics        = true
	defaultEnablePprof          = false
	defaultEnableSwagger        = true
	defaultUpstreamTimeout      = 0
	defaultAppRateLimit         = "20:1,100:120"

	// HTTP server tuning (internal)
	defaultReadHeaderTimeout = 10 * time.Second
	defaultReadTimeout       = 10 * time.Second
	defaultIdleTimeout       = 90 * time.Second
)

type Config struct {
	Tokens           []string
	Port             int
	QueueCapacity    int
	AdmissionTimeout time.Duration
	AdditionalWindow time.Duration
	ShutdownTimeout  time.Duration
	MetricsEnabled   bool
	PprofEnabled     bool
	SwaggerEnabled   bool
	UpstreamTimeout  time.Duration
	DefaultAppLimits string
	Server           ServerConfig
}

type ServerConfig struct {
	ReadHeaderTimeout time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
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
		UpstreamTimeout:  defaultUpstreamTimeout,
		DefaultAppLimits: defaultAppRateLimit,
		Server: ServerConfig{
			ReadHeaderTimeout: defaultReadHeaderTimeout,
			ReadTimeout:       defaultReadTimeout,
			IdleTimeout:       defaultIdleTimeout,
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

	mustParseRateLimit("DEFAULT_APP_RATE_LIMIT", &cfg.DefaultAppLimits, &errs)

	if cfg.Port > 65535 {
		errs = append(errs, fmt.Errorf("PORT must be <= 65535"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}

	// WriteTimeout must allow: queue wait (AdmissionTimeout) + upstream request + buffer
	upstreamBudget := cfg.UpstreamTimeout
	if upstreamBudget <= 0 {
		upstreamBudget = 5 * time.Minute
	}
	cfg.Server.WriteTimeout = cfg.AdmissionTimeout + upstreamBudget + 30*time.Second

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

func mustParseRateLimit(key string, dst *string, errs *[]error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return
	}

	parts := strings.Split(value, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		pair := strings.SplitN(part, ":", 2)
		if len(pair) != 2 {
			*errs = append(*errs, fmt.Errorf("%s must be in format 'limit:window,limit:window' (e.g., '20:1,100:120'): %s", key, part))
			return
		}
		limit, err1 := strconv.Atoi(strings.TrimSpace(pair[0]))
		window, err2 := strconv.Atoi(strings.TrimSpace(pair[1]))
		if err1 != nil || err2 != nil || limit <= 0 || window <= 0 {
			*errs = append(*errs, fmt.Errorf("%s contains invalid values (must be positive integers): %s", key, part))
			return
		}
	}
	*dst = value
}
