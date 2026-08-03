package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResponseRecorderPreservesFirstStatus(t *testing.T) {
	t.Parallel()

	underlying := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: underlying, statusCode: http.StatusOK}
	recorder.WriteHeader(http.StatusCreated)
	recorder.WriteHeader(http.StatusInternalServerError)

	if got, want := recorder.statusCode, http.StatusCreated; got != want {
		t.Errorf("recorded status = %d, want %d", got, want)
	}
	if got, want := underlying.Code, http.StatusCreated; got != want {
		t.Errorf("underlying status = %d, want %d", got, want)
	}
}

func TestResponseRecorderExposesUnderlyingControllerCapabilities(t *testing.T) {
	t.Parallel()

	underlying := httptest.NewRecorder()
	recorder := &responseRecorder{ResponseWriter: underlying, statusCode: http.StatusOK}
	if err := http.NewResponseController(recorder).Flush(); err != nil {
		t.Fatalf("ResponseController.Flush() error = %v", err)
	}
	if !underlying.Flushed {
		t.Fatal("underlying ResponseWriter was not flushed")
	}
}
