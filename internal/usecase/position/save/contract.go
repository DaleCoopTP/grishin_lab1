//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package save_test

package save

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type positionRepository interface {
	GetForUpdate(ctx context.Context, id uuid.UUID) (trip.Trip, error)
	InsertPosition(ctx context.Context, position trip.Position) (int64, error)
	TouchLastPosition(ctx context.Context, tripID uuid.UUID, recordedAt time.Time) error
}

type txManager interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

type pendingRequests interface {
	Cancel(tripID uuid.UUID)
}

type positionEvents interface {
	Saved(ctx context.Context, position trip.Position)
}
