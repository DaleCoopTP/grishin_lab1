//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package create_test

package create

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type usecase interface {
	Save(ctx context.Context, tripID uuid.UUID, point trip.Point, recordedAt time.Time) (trip.Position, error)
}
