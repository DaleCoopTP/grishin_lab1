package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

const (
	resultKey     = "result"
	resultDropped = "dropped"
)

type PositionEvents struct {
	logger *slog.Logger
	saved  metric.Int64Counter
}

func NewPositionEvents(meter metric.Meter, logger *slog.Logger) (*PositionEvents, error) {
	saved, err := meter.Int64Counter("tripgo.positions.saved", metric.WithUnit("{position}"))
	if err != nil {
		return nil, fmt.Errorf("create saved positions counter: %w", err)
	}
	return &PositionEvents{logger: logger, saved: saved}, nil
}

func (e *PositionEvents) Saved(ctx context.Context, position trip.Position) {
	e.saved.Add(ctx, 1)
	e.logger.DebugContext(ctx, "position saved", "trip_id", position.TripID, "position_id", position.ID)
}

type positionPool interface {
	QueueSize() int
	ActiveWorkers() int
}

type PositionRequests struct {
	meter       metric.Meter
	staleChecks metric.Int64Counter
	scanRows    metric.Int64Histogram
	requests    metric.Int64Counter
	duration    metric.Float64Histogram
}

func NewPositionRequests(meter metric.Meter) (*PositionRequests, error) {
	staleChecks, err := meter.Int64Counter("tripgo.stale_checks", metric.WithUnit("{trip}"))
	if err != nil {
		return nil, fmt.Errorf("create stale checks counter: %w", err)
	}
	scanRows, err := meter.Int64Histogram("tripgo.stale_checks.rows",
		metric.WithUnit("{row}"),
		metric.WithDescription("stale trip rows read by one scanner tick"),
		metric.WithExplicitBucketBoundaries(0, 10, 50, 100, 250, 500, 1000, 2500, 5000, 10000, 50000),
	)
	if err != nil {
		return nil, fmt.Errorf("create stale check rows histogram: %w", err)
	}
	requests, err := meter.Int64Counter("tripgo.position_requests", metric.WithUnit("{request}"))
	if err != nil {
		return nil, fmt.Errorf("create position requests counter: %w", err)
	}
	duration, err := meter.Float64Histogram("tripgo.position_requests.duration",
		metric.WithUnit("s"),
		metric.WithExplicitBucketBoundaries(0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 0.75, 1, 1.1, 1.25, 1.5, 2, 2.5, 5, 10),
	)
	if err != nil {
		return nil, fmt.Errorf("create position request duration histogram: %w", err)
	}
	return &PositionRequests{meter: meter, staleChecks: staleChecks, scanRows: scanRows, requests: requests, duration: duration}, nil
}

func (p *PositionRequests) ObservePool(pool positionPool) error {
	queueSize, err := p.meter.Int64ObservableGauge("tripgo.position_requests.queue.size", metric.WithUnit("{request}"))
	if err != nil {
		return fmt.Errorf("create position queue size gauge: %w", err)
	}
	active, err := p.meter.Int64ObservableGauge("tripgo.position_requests.workers.active", metric.WithUnit("{worker}"))
	if err != nil {
		return fmt.Errorf("create active position workers gauge: %w", err)
	}
	if _, err := p.meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		observer.ObserveInt64(queueSize, int64(pool.QueueSize()))
		observer.ObserveInt64(active, int64(pool.ActiveWorkers()))
		return nil
	}, queueSize, active); err != nil {
		return fmt.Errorf("register position pool gauges: %w", err)
	}
	return nil
}

func (p *PositionRequests) StaleCheck(ctx context.Context, result string) {
	p.staleChecks.Add(ctx, 1, metric.WithAttributes(attribute.String(resultKey, result)))
}

func (p *PositionRequests) ScanFinished(ctx context.Context, rows int) {
	p.scanRows.Record(ctx, int64(rows))
}

func (p *PositionRequests) RequestDropped(ctx context.Context) {
	p.requests.Add(ctx, 1, metric.WithAttributes(attribute.String(resultKey, resultDropped)))
}

func (p *PositionRequests) RequestFinished(ctx context.Context, result string, duration time.Duration) {
	attrs := metric.WithAttributes(attribute.String(resultKey, result))
	p.requests.Add(ctx, 1, attrs)
	p.duration.Record(ctx, duration.Seconds(), attrs)
}
