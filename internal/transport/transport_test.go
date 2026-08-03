package transport

import (
	"context"
	"io"
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

func TestWithRequestTimeoutKeepsContextAliveUntilBodyClose(t *testing.T) {
	t.Parallel()

	var requestContext context.Context
	rt := WithRequestTimeout(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
		requestContext = r.Context()
		return testutil.HTTPResponse(http.StatusOK, "payload", nil), nil
	}), time.Second)

	resp, err := rt.RoundTrip(httptestRequest(t))
	if err != nil {
		t.Fatalf("RoundTrip() error = %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("ReadAll(response body) error = %v", err)
	}
	if got, want := string(body), "payload"; got != want {
		t.Fatalf("response body = %q, want %q", got, want)
	}
	if err := requestContext.Err(); err != nil {
		t.Fatalf("request context before Body.Close() error = %v, want nil", err)
	}
	if err := resp.Body.Close(); err != nil {
		t.Fatalf("Body.Close() error = %v", err)
	}
	if got, want := requestContext.Err(), context.Canceled; got != want {
		t.Fatalf("request context after Body.Close() error = %v, want %v", got, want)
	}
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

	t.Run("notifies observer before retry", func(t *testing.T) {
		t.Parallel()

		attempts := 0
		observed := 0
		rt := WithRetryAfter429(testutil.RoundTripperFunc(func(r *http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return testutil.HTTPResponse(http.StatusTooManyRequests, "", http.Header{
					"Retry-After": []string{"0"},
				}), nil
			}
			return testutil.HTTPResponse(http.StatusNoContent, "", nil), nil
		}), 1, func(req *http.Request, resp *http.Response) {
			if got, want := req.URL.String(), "https://example.invalid/test"; got != want {
				t.Errorf("observer request URL = %q, want %q", got, want)
			}
			if got, want := resp.StatusCode, http.StatusTooManyRequests; got != want {
				t.Errorf("observer status = %d, want %d", got, want)
			}
			observed++
		})

		resp, err := rt.RoundTrip(httptestRequest(t))
		if err != nil {
			t.Fatalf("RoundTrip() error = %v", err)
		}
		defer resp.Body.Close()

		if got, want := attempts, 2; got != want {
			t.Errorf("attempts = %d, want %d", got, want)
		}
		if got, want := observed, 1; got != want {
			t.Errorf("observer calls = %d, want %d", got, want)
		}
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
