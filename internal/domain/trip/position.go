package trip

import (
	"time"

	"github.com/google/uuid"
)

const MaxRecordedAtSkew = time.Minute

type Position struct {
	ID         int64
	TripID     uuid.UUID
	Point      Point
	RecordedAt time.Time
}

type StaleTrip struct {
	ID         uuid.UUID
	DriverID   uuid.UUID
	LastSeenAt time.Time
}

func NewPosition(tripID uuid.UUID, point Point, recordedAt, now time.Time) (Position, error) {
	if tripID == uuid.Nil {
		return Position{}, &ValidationError{Field: "trip_id", Reason: reasonZeroValue}
	}
	if err := validatePoint(point, "coordinates"); err != nil {
		return Position{}, err
	}
	if recordedAt.IsZero() {
		return Position{}, &ValidationError{Field: "recorded_at", Reason: reasonZeroValue}
	}
	if recordedAt.After(now.Add(MaxRecordedAtSkew)) {
		return Position{}, &ValidationError{Field: "recorded_at", Reason: "must not be more than a minute in the future"}
	}
	return Position{
		TripID:     tripID,
		Point:      point,
		RecordedAt: recordedAt.UTC().Truncate(time.Microsecond),
	}, nil
}
