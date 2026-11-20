package push_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"

	"github.com/DaleCoopTP/grishin_lab1/internal/gateway/push"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func newServer(t *testing.T, handler http.HandlerFunc) *push.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	client := push.New(server.URL, time.Second)
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client
}

func TestRequestPositionSendsCommand(t *testing.T) {
	driverID, tripID := uuid.New(), uuid.New()
	var got struct {
		RequestID   string            `json:"request_id"`
		RecipientID uuid.UUID         `json:"recipient_id"`
		Kind        string            `json:"kind"`
		Data        map[string]string `json:"data"`
	}
	client := newServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/push" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected request %s %s %q", r.Method, r.URL.Path, r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"message_id":"m-1","accepted_at":"2026-08-24T12:00:00Z"}`)
	})

	if err := client.RequestPosition(t.Context(), driverID, tripID); err != nil {
		t.Fatal(err)
	}
	if got.RecipientID != driverID || got.Kind != "REQUEST_POSITION" || got.Data["trip_id"] != tripID.String() || len(got.Data) != 1 {
		t.Fatalf("unexpected push command: %+v", got)
	}
	if _, err := uuid.Parse(got.RequestID); err != nil {
		t.Fatalf("request_id is not generated: %q", got.RequestID)
	}
}

func TestRequestPositionReturnsStatusError(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest, http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := newServer(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = io.WriteString(w, "details that must not be read entirely")
			})
			err := client.RequestPosition(t.Context(), uuid.New(), uuid.New())
			var statusErr *push.StatusError
			if !errors.As(err, &statusErr) || statusErr.StatusCode != status {
				t.Fatalf("got %v, want status error %d", err, status)
			}
		})
	}
}

func drainBody(r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
}

func TestRequestPositionTransportErrors(t *testing.T) {
	t.Run("connection refused", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		client := push.New(server.URL, time.Second)
		defer client.Close()
		err := client.RequestPosition(t.Context(), uuid.New(), uuid.New())
		var statusErr *push.StatusError
		if err == nil || errors.As(err, &statusErr) {
			t.Fatalf("got %v, want transport error", err)
		}
	})
	t.Run("context cancelled", func(t *testing.T) {
		received := make(chan struct{})
		client := newServer(t, func(_ http.ResponseWriter, r *http.Request) {
			drainBody(r)
			close(received)
			<-r.Context().Done()
		})
		ctx, cancel := context.WithCancel(t.Context())
		go func() {
			<-received
			cancel()
		}()
		err := client.RequestPosition(ctx, uuid.New(), uuid.New())
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want context.Canceled", err)
		}
	})
	t.Run("client timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			drainBody(r)
			<-r.Context().Done()
		}))
		client := push.New(server.URL, 20*time.Millisecond)
		defer server.Close()
		defer client.Close()
		err := client.RequestPosition(t.Context(), uuid.New(), uuid.New())
		var netErr interface{ Timeout() bool }
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("got %v, want timeout", err)
		}
	})
}

type closeTracker struct {
	io.Reader
	closed *atomic.Bool
}

func (b closeTracker) Close() error {
	b.closed.Store(true)
	return nil
}

type stubTransport struct {
	status int
	closed *atomic.Bool
}

func (s stubTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: s.status,
		Body:       closeTracker{Reader: strings.NewReader(strings.Repeat("x", 1<<20)), closed: s.closed},
		Header:     http.Header{},
	}, nil
}

func TestRequestPositionAlwaysClosesBody(t *testing.T) {
	for _, status := range []int{http.StatusAccepted, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			closed := &atomic.Bool{}
			client := push.New("http://push.invalid", time.Second, push.WithTransport(func(http.RoundTripper) http.RoundTripper {
				return stubTransport{status: status, closed: closed}
			}))
			defer client.Close()
			_ = client.RequestPosition(t.Context(), uuid.New(), uuid.New())
			if !closed.Load() {
				t.Fatal("response body was not closed")
			}
		})
	}
}
