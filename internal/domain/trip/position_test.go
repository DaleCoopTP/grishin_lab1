package trip_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

const (
	fieldLatitude  = "coordinates.latitude"
	fieldLongitude = "coordinates.longitude"
)

func TestNewPosition(t *testing.T) {
	tripID := uuid.New()
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	zone := time.FixedZone("MSK", 3*60*60)
	cases := []struct {
		name       string
		point      trip.Point
		recordedAt time.Time
		want       time.Time
	}{
		{"past", trip.Point{Latitude: 59.935, Longitude: 30.335}, now.Add(-time.Hour), now.Add(-time.Hour)},
		{"boundary coordinates", trip.Point{Latitude: -90, Longitude: 180}, now, now},
		{"exactly one minute ahead", trip.Point{Latitude: 90, Longitude: -180}, now.Add(time.Minute), now.Add(time.Minute)},
		{"other zone becomes UTC", trip.Point{}, now.In(zone), now},
		{"nanoseconds are truncated", trip.Point{}, now.Add(1999 * time.Nanosecond), now.Add(time.Microsecond)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := trip.NewPosition(tripID, tc.point, tc.recordedAt, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != 0 || got.TripID != tripID || got.Point != tc.point {
				t.Fatalf("constructor did not preserve input: %+v", got)
			}
			if !got.RecordedAt.Equal(tc.want) || got.RecordedAt.Location() != time.UTC {
				t.Fatalf("recorded_at: got %s, want %s in UTC", got.RecordedAt, tc.want)
			}
		})
	}
}

func TestNewPositionInvalidInput(t *testing.T) {
	now := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	valid := trip.Position{
		TripID:     uuid.New(),
		Point:      trip.Point{Latitude: 59.935, Longitude: 30.335},
		RecordedAt: now,
	}
	cases := []struct {
		name   string
		field  string
		change func(*trip.Position)
	}{
		{"zero trip", "trip_id", func(v *trip.Position) { v.TripID = uuid.Nil }},
		{"latitude below range", fieldLatitude, func(v *trip.Position) { v.Point.Latitude = -90.01 }},
		{"latitude above range", fieldLatitude, func(v *trip.Position) { v.Point.Latitude = 91 }},
		{"longitude below range", fieldLongitude, func(v *trip.Position) { v.Point.Longitude = -181 }},
		{"longitude above range", fieldLongitude, func(v *trip.Position) { v.Point.Longitude = 180.01 }},
		{"NaN latitude", fieldLatitude, func(v *trip.Position) { v.Point.Latitude = math.NaN() }},
		{"infinite longitude", fieldLongitude, func(v *trip.Position) { v.Point.Longitude = math.Inf(-1) }},
		{"zero time", "recorded_at", func(v *trip.Position) { v.RecordedAt = time.Time{} }},
		{"too far in the future", "recorded_at", func(v *trip.Position) { v.RecordedAt = now.Add(time.Minute + time.Nanosecond) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := valid
			tc.change(&input)
			_, err := trip.NewPosition(input.TripID, input.Point, input.RecordedAt, now)
			var validationError *trip.ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("got %v, want validation error", err)
			}
			if validationError.Field != tc.field {
				t.Fatalf("field: got %q, want %q", validationError.Field, tc.field)
			}
		})
	}
}
