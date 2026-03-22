package transport

import (
	"context"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/renja-g/RiftRelay/internal/testutil"
)

func TestWithRequestTimeout(t *testing.T) {
	t.Parallel()

	const timeout = 250 * time.Millisecond

	rt := WithRequestTimeout(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("Context().Deadline() ok = false, want true")
		}
		if got := time.Until(deadline); got <= 0 || got > timeout {
			t.Fatalf("time.Until(deadline) = %v, want (0,%v]", got, timeout)
		}
		return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
	}), timeout)

	resp, err := rt.RoundTrip(httptestRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	_ = resp.Body.Close()
}

func TestWithRetryAfter429(t *testing.T) {
	t.Run("retries after retry-after response", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			attempts := 0
			rt := WithRetryAfter429(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				attempts++
				if attempts == 1 {
					return testutil.HTTPResponse(http.StatusTooManyRequests, "", http.Header{
						"Retry-After": []string{"2"},
					}), nil
				}
				return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
			}), 2)

			done := make(chan error, 1)
			go func() {
				resp, err := rt.RoundTrip(httptestRequest(t))
				if err == nil {
					_ = resp.Body.Close()
				}
				done <- err
			}()

			synctest.Wait()
			time.Sleep(2 * time.Second)
			synctest.Wait()

			if err := <-done; err != nil {
				t.Fatalf("RoundTrip() error = %v", err)
			}
			if got, want := attempts, 2; got != want {
				t.Fatalf("attempts = %d, want %d", got, want)
			}
		})
	})

	t.Run("honors context cancellation while waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rt := WithRetryAfter429(testutil.RoundTripperFunc(func(*http.Request) (*http.Response, error) {
				return testutil.HTTPResponse(http.StatusTooManyRequests, "", http.Header{
					"Retry-After": []string{"10"},
				}), nil
			}), 2)

			ctx, cancel := context.WithCancel(context.Background())
			req := httptestRequest(t).Clone(ctx)

			done := make(chan error, 1)
			go func() {
				_, err := rt.RoundTrip(req)
				done <- err
			}()

			synctest.Wait()
			cancel()
			synctest.Wait()

			if err := <-done; err != context.Canceled {
				t.Fatalf("RoundTrip() error = %v, want %v", err, context.Canceled)
			}
		})
	})
}

func httptestRequest(t *testing.T) *http.Request {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.invalid/test", nil)
	if err != nil {
		t.Fatalf("http.NewRequestWithContext() error = %v", err)
	}
	return req
}
