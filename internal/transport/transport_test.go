package transport

import "testing"

func TestNewEnablesHTTP2HealthChecks(t *testing.T) {
	t.Parallel()

	tr := New()
	if !tr.ForceAttemptHTTP2 {
		t.Fatal("ForceAttemptHTTP2 = false, want true")
	}
	if tr.HTTP2 == nil {
		t.Fatal("HTTP2 = nil, want health check config")
	}
	if got, want := tr.HTTP2.SendPingTimeout, defaultHTTP2SendPingTimeout; got != want {
		t.Fatalf("HTTP2.SendPingTimeout = %v, want %v", got, want)
	}
	if got, want := tr.HTTP2.PingTimeout, defaultHTTP2PingTimeout; got != want {
		t.Fatalf("HTTP2.PingTimeout = %v, want %v", got, want)
	}
}
