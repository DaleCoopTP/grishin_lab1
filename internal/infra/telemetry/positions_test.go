package telemetry_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/infra/telemetry"
)

type poolState struct{ queued, active int }

func (p poolState) QueueSize() int     { return p.queued }
func (p poolState) ActiveWorkers() int { return p.active }

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Aggregation {
	t.Helper()
	var data metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &data); err != nil {
		t.Fatal(err)
	}
	metrics := map[string]metricdata.Aggregation{}
	for _, scope := range data.ScopeMetrics {
		for _, m := range scope.Metrics {
			metrics[m.Name] = m.Data
		}
	}
	return metrics
}

func sumByResult(t *testing.T, data metricdata.Aggregation) map[string]int64 {
	t.Helper()
	sum, ok := data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("not an int64 sum: %T", data)
	}
	got := map[string]int64{}
	for _, point := range sum.DataPoints {
		result, _ := point.Attributes.Value(attribute.Key("result"))
		got[result.AsString()] = point.Value
	}
	return got
}

func TestPositionRequestsMetrics(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	requests, err := telemetry.NewPositionRequests(meter)
	if err != nil {
		t.Fatal(err)
	}
	if err := requests.ObservePool(poolState{queued: 7, active: 3}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, result := range []string{"queued", "queued", "duplicate", "dropped"} {
		requests.StaleCheck(ctx, result)
	}
	requests.RequestDropped(ctx)
	requests.RequestFinished(ctx, "success", 150*time.Millisecond)
	requests.RequestFinished(ctx, "error", time.Second)

	metrics := collect(t, reader)
	if got := sumByResult(t, metrics["tripgo.stale_checks"]); got["queued"] != 2 || got["duplicate"] != 1 || got["dropped"] != 1 {
		t.Fatalf("stale checks: %v", got)
	}
	if got := sumByResult(t, metrics["tripgo.position_requests"]); got["success"] != 1 || got["error"] != 1 || got["dropped"] != 1 {
		t.Fatalf("position requests: %v", got)
	}
	histogram, ok := metrics["tripgo.position_requests.duration"].(metricdata.Histogram[float64])
	if !ok || len(histogram.DataPoints) != 2 {
		t.Fatalf("duration histogram: %+v", metrics["tripgo.position_requests.duration"])
	}
	for _, point := range histogram.DataPoints {
		result, _ := point.Attributes.Value(attribute.Key("result"))
		if want := map[string]float64{"success": 0.15, "error": 1}[result.AsString()]; point.Count != 1 || point.Sum != want {
			t.Fatalf("duration for %s: %+v", result.AsString(), point)
		}
	}
	for name, want := range map[string]int64{"tripgo.position_requests.queue.size": 7, "tripgo.position_requests.workers.active": 3} {
		gauge, ok := metrics[name].(metricdata.Gauge[int64])
		if !ok || len(gauge.DataPoints) != 1 || gauge.DataPoints[0].Value != want {
			t.Fatalf("%s: %+v", name, metrics[name])
		}
	}
}

func TestPositionEventsCountsSavedPositions(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)).Meter("test")
	events, err := telemetry.NewPositionEvents(meter, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	events.Saved(context.Background(), trip.Position{ID: 1, TripID: uuid.New()})
	events.Saved(context.Background(), trip.Position{ID: 2, TripID: uuid.New()})
	sum, ok := collect(t, reader)["tripgo.positions.saved"].(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 || sum.DataPoints[0].Value != 2 {
		t.Fatalf("saved positions: %+v", sum)
	}
}
