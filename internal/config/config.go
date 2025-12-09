package config

import (
	"fmt"
	"os"
)

const (
	defaultPort       = "8080"
	defaultMaxRetries = 2
)

type Config struct {
	Token      string
	Port       string
	MaxRetries int
}

func Load() (Config, error) {
	cfg := Config{
		Token:      os.Getenv("RIOT_TOKEN"),
		Port:       os.Getenv("PORT"),
		MaxRetries: defaultMaxRetries,
	}

	if cfg.Token == "" {
		return Config{}, fmt.Errorf("RIOT_TOKEN env var is required")
	}

	if cfg.Port == "" {
		cfg.Port = defaultPort
	}

	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = defaultMaxRetries
	}

	return cfg, nil
}
