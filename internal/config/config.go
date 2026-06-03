package config

import (
	"errors"
	"fmt"
	"math"
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
	RateBudgets      map[string]RateBudget
	Server           ServerConfig
}

type RateBudget struct {
	Share        float64
	BucketShares map[string]float64
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
	cfg.RateBudgets = parseRateBudgets(&errs)

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

func (c Config) RateBudgetShare(id, bucket string) (float64, bool) {
	id = strings.TrimSpace(id)
	if id == "" || id == "default" {
		return 1, true
	}

	budget, ok := c.RateBudgets[id]
	if !ok || budget.Share <= 0 {
		return 0, false
	}

	if share, ok := budget.BucketShares[bucket]; ok {
		return share, true
	}
	if idx := strings.IndexByte(bucket, ':'); idx >= 0 {
		if share, ok := budget.BucketShares[bucket[idx+1:]]; ok {
			return share, true
		}
	}

	return budget.Share, true
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

func parseRateBudgets(errs *[]error) map[string]RateBudget {
	const prefix = "RATE_BUDGET_"
	const overridesSuffix = "_OVERRIDES"

	budgets := make(map[string]RateBudget)
	for _, env := range os.Environ() {
		key, value, ok := strings.Cut(env, "=")
		if !ok || !strings.HasPrefix(key, prefix) {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		name := strings.TrimPrefix(key, prefix)
		if strings.HasSuffix(name, overridesSuffix) {
			id := strings.TrimSuffix(name, overridesSuffix)
			if !validRateBudgetID(id) || id == "default" {
				*errs = append(*errs, fmt.Errorf("%s has invalid budget id %q", key, id))
				continue
			}
			addRateBudgetOverrides(key, id, value, budgets, errs)
			continue
		}

		id, bucket, hasBucket := strings.Cut(name, ":")
		bucket = strings.TrimSpace(bucket)
		if !validRateBudgetID(id) || id == "default" {
			*errs = append(*errs, fmt.Errorf("%s has invalid budget id %q", key, id))
			continue
		}
		if hasBucket && bucket == "" {
			*errs = append(*errs, fmt.Errorf("%s must include a bucket after ':'", key))
			continue
		}

		share, ok := parseRateBudgetShare(key, value, errs)
		if !ok {
			continue
		}

		budget := budgets[id]
		if hasBucket {
			if budget.BucketShares == nil {
				budget.BucketShares = make(map[string]float64)
			}
			budget.BucketShares[bucket] = share
		} else {
			budget.Share = share
		}
		budgets[id] = budget
	}

	for id, budget := range budgets {
		if budget.Share <= 0 {
			*errs = append(*errs, fmt.Errorf("RATE_BUDGET_%s must be set when bucket overrides are configured", id))
		}
	}
	if len(budgets) == 0 {
		return nil
	}
	return budgets
}

func addRateBudgetOverrides(key, id, value string, budgets map[string]RateBudget, errs *[]error) {
	budget := budgets[id]
	if budget.BucketShares == nil {
		budget.BucketShares = make(map[string]float64)
	}

	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}

		bucket, rawShare, ok := strings.Cut(part, "=")
		bucket = strings.TrimSpace(bucket)
		if !ok || bucket == "" {
			*errs = append(*errs, fmt.Errorf("%s overrides must be in format 'bucket=share,bucket=share'", key))
			continue
		}

		share, ok := parseRateBudgetShare(key, strings.TrimSpace(rawShare), errs)
		if !ok {
			continue
		}
		budget.BucketShares[bucket] = share
	}

	budgets[id] = budget
}

func parseRateBudgetShare(key, value string, errs *[]error) (float64, bool) {
	share, err := strconv.ParseFloat(value, 64)
	if err != nil || share <= 0 || share > 1 || math.IsNaN(share) || math.IsInf(share, 0) {
		*errs = append(*errs, fmt.Errorf("%s must be a number > 0 and <= 1", key))
		return 0, false
	}
	return share, true
}

func validRateBudgetID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}
