package testutil

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/renja-g/RiftRelay/internal/config"
)

type RoundTripperFunc func(*http.Request) (*http.Response, error)

func (fn RoundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}

func DummyConfig() config.Config {
	return config.Config{
		Tokens:           []string{"test-token-a", "test-token-b"},
		Port:             8985,
		QueueCapacity:    8,
		AdmissionTimeout: 2 * time.Second,
		AdditionalWindow: 150 * time.Millisecond,
		ShutdownTimeout:  time.Second,
		MetricsEnabled:   true,
		PprofEnabled:     false,
		SwaggerEnabled:   true,
		UpstreamTimeout:  250 * time.Millisecond,
		DefaultAppLimits: "20:1,100:120",
		Server: config.ServerConfig{
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      45 * time.Second,
			IdleTimeout:       90 * time.Second,
		},
	}
}

func HTTPResponse(statusCode int, body string, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}

	return &http.Response{
		StatusCode: statusCode,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func HTTPResponseBytes(statusCode int, body []byte, headers http.Header) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}

	return &http.Response{
		StatusCode: statusCode,
		Header:     headers,
		Body:       io.NopCloser(bytes.NewReader(body)),
	}
}
