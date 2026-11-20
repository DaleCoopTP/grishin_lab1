package create

import (
	"net/http"

	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/request"
)

func decodeCreateRequest(w http.ResponseWriter, r *http.Request, body *api.TripData) error {
	fields, err := request.DecodeJSON(w, r, body, "user_id", "driver_id", "start_point", "end_point", "price")
	if err != nil {
		return err
	}
	for _, point := range []string{"start_point", "end_point"} {
		if err := request.RequireFields(fields, point, "latitude", "longitude"); err != nil {
			return err
		}
	}
	return nil
}
