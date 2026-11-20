package position

import (
	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
)

func ToResponse(position trip.Position) api.TripPosition {
	return api.TripPosition{
		Id:          position.ID,
		TripId:      position.TripID,
		Coordinates: api.Coordinates{Latitude: position.Point.Latitude, Longitude: position.Point.Longitude},
		RecordedAt:  position.RecordedAt,
	}
}
