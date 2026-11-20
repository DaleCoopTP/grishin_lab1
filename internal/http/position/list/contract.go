//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package list_test

package list

import (
	"context"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type usecase interface {
	List(ctx context.Context, tripID uuid.UUID) ([]trip.Position, error)
}
