package create

import (
	"net/http"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/domainerror"
	positionresponse "github.com/DaleCoopTP/grishin_lab1/internal/http/position"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/request"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/response"
)

type Handler struct {
	usecase  usecase
	response *response.Writer
}

func New(usecase usecase, response *response.Writer) *Handler {
	return &Handler{usecase: usecase, response: response}
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request, tripID api.TripId) {
	var body api.PositionData
	fields, err := request.DecodeJSON(w, r, &body, "coordinates", "recorded_at")
	if err == nil {
		err = request.RequireFields(fields, "coordinates", "latitude", "longitude")
	}
	if err != nil {
		h.response.Error(w, r, domainerror.ErrInvalidRequest)
		return
	}
	point := trip.Point{Latitude: body.Coordinates.Latitude, Longitude: body.Coordinates.Longitude}
	position, err := h.usecase.Save(r.Context(), tripID, point, body.RecordedAt)
	if err != nil {
		h.response.Error(w, r, err)
		return
	}
	h.response.JSON(w, r, http.StatusCreated, positionresponse.ToResponse(position))
}
