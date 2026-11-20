package list

import (
	"net/http"

	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
	positionresponse "github.com/DaleCoopTP/grishin_lab1/internal/http/position"
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
	positions, err := h.usecase.List(r.Context(), tripID)
	if err != nil {
		h.response.Error(w, r, err)
		return
	}
	body := api.TripPositionsResponse{Positions: make([]api.TripPosition, 0, len(positions))}
	for _, position := range positions {
		body.Positions = append(body.Positions, positionresponse.ToResponse(position))
	}
	h.response.JSON(w, r, http.StatusOK, body)
}
