package proxy

import "net/http"

// RequestGate can enforce admission control (rate limits, circuit breakers, etc.)
// by wrapping the proxy handler.
type RequestGate interface {
	Wrap(next http.Handler) http.Handler
}

// Scheduler can reorder or queue requests before they reach the proxy handler.
type Scheduler interface {
	Wrap(next http.Handler) http.Handler
}

// MiddlewareFromGate adapts a RequestGate to a Middleware.
func MiddlewareFromGate(gate RequestGate) Middleware {
	return func(next http.Handler) http.Handler {
		return gate.Wrap(next)
	}
}

// MiddlewareFromScheduler adapts a Scheduler to a Middleware.
func MiddlewareFromScheduler(s Scheduler) Middleware {
	return func(next http.Handler) http.Handler {
		return s.Wrap(next)
	}
}
