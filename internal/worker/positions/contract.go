//go:generate go tool mockgen -source contract.go -destination mocks_test.go -package positions_test

package positions

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type staleReader interface {
	ListStale(ctx context.Context, staleAfter time.Duration) ([]trip.StaleTrip, error)
}

type pusher interface {
	RequestPosition(ctx context.Context, driverID, tripID uuid.UUID) error
}

type metrics interface {
	StaleCheck(ctx context.Context, result string)
	RequestDropped(ctx context.Context)
	RequestFinished(ctx context.Context, result string, duration time.Duration)
}

// Ticker hides time.Ticker so tests can drive the scanner tick by tick.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}
