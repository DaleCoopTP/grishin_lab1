package telemetry

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel/metric"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
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
