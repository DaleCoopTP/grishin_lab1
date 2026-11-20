package positions_test

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/worker/positions"
)

func TestRequestSpanIsLinkedToScanSpan(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := staleTrips(2)
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), trips[0].DriverID, trips[0].ID).Return(nil)
	push.EXPECT().RequestPosition(gomock.Any(), trips[1].DriverID, trips[1].ID).Return(errors.New("push service unavailable"))
	m, _ := recordingMetrics(ctrl)
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	ticker := newManualTicker()
	svc := positions.New(config(1, 10), scriptedReader(ctrl, found(trips...)), push, m, slog.New(slog.DiscardHandler),
		positions.WithTicker(func(time.Duration) positions.Ticker { return ticker }),
		positions.WithTracer(provider.Tracer("test")))
	svc.Start(context.Background())
	ticker.tick(t)
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	var scans, requests []sdktrace.ReadOnlySpan
	for _, span := range recorder.Ended() {
		switch span.Name() {
		case "positions.scan":
			scans = append(scans, span)
		case "positions.request":
			requests = append(requests, span)
		}
	}
	if len(scans) == 0 || len(requests) != 2 {
		t.Fatalf("got %d scan and %d request spans", len(scans), len(requests))
	}
	scan := scans[0]
	failed := 0
	for _, request := range requests {
		if request.Parent().IsValid() || request.SpanContext().TraceID() == scan.SpanContext().TraceID() {
			t.Fatal("request span must start a new trace instead of being a child of the scan")
		}
		links := request.Links()
		if len(links) != 1 || links[0].SpanContext.SpanID() != scan.SpanContext().SpanID() {
			t.Fatalf("request span is not linked to the scan span: %+v", links)
		}
		if request.Status().Code == codes.Error {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("exactly one request span must be marked as failed, got %d", failed)
	}
}
