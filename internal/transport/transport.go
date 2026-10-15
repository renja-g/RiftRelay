package transport

import (
	"net"
	"net/http"
	"time"
)

const (
	defaultDialTimeout           = 5 * time.Second
	defaultDialKeepAlive         = 30 * time.Second
	defaultMaxIdleConns          = 512
	defaultMaxIdleConnsPerHost   = 256
	defaultIdleConnTimeout       = 90 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultExpectContinueTimeout = 1 * time.Second
	defaultResponseHeaderTimeout = 15 * time.Second
	// An HTTP/2 connection that sends no frame for this long is pinged, and
	// closed if the ping is not answered, so its streams fail fast instead of
	// stalling until their deadlines.
	defaultHTTP2SendPingTimeout = 10 * time.Second
	defaultHTTP2PingTimeout     = 5 * time.Second
)

func New() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   defaultDialTimeout,
		KeepAlive: defaultDialKeepAlive,
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          defaultMaxIdleConns,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		MaxConnsPerHost:       0,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		HTTP2: &http.HTTP2Config{
			SendPingTimeout: defaultHTTP2SendPingTimeout,
			PingTimeout:     defaultHTTP2PingTimeout,
		},
	}
}
