package list_test

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/position/list"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/response"
)

func TestHandler(t *testing.T) {
	tripID := uuid.New()
	at := time.Date(2026, time.August, 24, 12, 0, 0, 0, time.UTC)
	route := []trip.Position{
		{ID: 7, TripID: tripID, Point: trip.Point{Latitude: 1, Longitude: 2}, RecordedAt: at},
		{ID: 3, TripID: tripID, Point: trip.Point{Latitude: 3, Longitude: 4}, RecordedAt: at.Add(time.Second)},
	}
	cases := []struct {
		name      string
		positions []trip.Position
		err       error
		status    int
		body      string
	}{
		{name: "route", positions: route, status: 200},
		{name: "empty route", positions: []trip.Position{}, status: 200, body: `{"positions":[]}`},
		{name: "nil route", positions: nil, status: 200, body: `{"positions":[]}`},
		{name: "not found", err: trip.ErrNotFound, status: 404},
		{name: "internal", err: errors.New("private database details"), status: 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usecase := NewMockusecase(gomock.NewController(t))
			usecase.EXPECT().List(gomock.Any(), tripID).Return(tc.positions, tc.err)
			req := httptest.NewRequest("GET", "/api/v1/trips/"+tripID.String()+"/positions", nil)
			out := httptest.NewRecorder()
			list.New(usecase, response.New()).Handle(out, req, tripID)
			if out.Code != tc.status || strings.Contains(out.Body.String(), "private") {
				t.Fatalf("status %d, body %s", out.Code, out.Body.String())
			}
			if tc.body != "" && strings.TrimSpace(out.Body.String()) != tc.body {
				t.Fatalf("body %s, want %s", out.Body.String(), tc.body)
			}
			if tc.err != nil || len(tc.positions) == 0 {
				return
			}
			var body api.TripPositionsResponse
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Positions) != 2 || body.Positions[0].Id != 7 || body.Positions[1].Id != 3 ||
				body.Positions[1].Coordinates.Latitude != 3 || !body.Positions[1].RecordedAt.Equal(at.Add(time.Second)) {
				t.Fatalf("route order or content changed: %s", out.Body.String())
			}
		})
	}
}
