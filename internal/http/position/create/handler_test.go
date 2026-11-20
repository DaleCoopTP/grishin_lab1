package create_test

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
	"github.com/DaleCoopTP/grishin_lab1/internal/http/position/create"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/response"
)

const (
	jsonContentType = "application/json"
	validBody       = `{"coordinates":{"latitude":59.935,"longitude":30.335},"recorded_at":"2026-08-24T15:00:05+03:00"}`
)

func TestHandler(t *testing.T) {
	tripID := uuid.New()
	point := trip.Point{Latitude: 59.935, Longitude: 30.335}
	recordedAt := time.Date(2026, time.August, 24, 12, 0, 5, 0, time.UTC)
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"created", nil, 201, ""},
		{"not found", trip.ErrNotFound, 404, "trip_not_found"},
		{"completed", trip.ErrCompleted, 409, "trip_completed"},
		{"domain validation", &trip.ValidationError{Field: "recorded_at", Reason: "future"}, 400, "invalid_request"},
		{"internal", errors.New("private database details"), 500, "internal_error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usecase := NewMockusecase(gomock.NewController(t))
			result := trip.Position{ID: 42, TripID: tripID, Point: point, RecordedAt: recordedAt}
			usecase.EXPECT().Save(gomock.Any(), tripID, point, gomock.Any()).DoAndReturn(
				func(_ any, _ uuid.UUID, _ trip.Point, at time.Time) (trip.Position, error) {
					if !at.Equal(recordedAt) {
						t.Fatalf("recorded_at: got %s, want %s", at, recordedAt)
					}
					return result, tc.err
				})
			req := httptest.NewRequest("POST", "/api/v1/trips/"+tripID.String()+"/positions", strings.NewReader(validBody))
			req.Header.Set("Content-Type", jsonContentType)
			out := httptest.NewRecorder()
			create.New(usecase, response.New()).Handle(out, req, tripID)

			if out.Code != tc.status {
				t.Fatalf("status %d, body %s", out.Code, out.Body.String())
			}
			if tc.err != nil {
				var problem api.Problem
				if err := json.Unmarshal(out.Body.Bytes(), &problem); err != nil || problem.Code != tc.code ||
					strings.Contains(out.Body.String(), "private") {
					t.Fatalf("unexpected Problem: %s", out.Body.String())
				}
				return
			}
			var body api.TripPosition
			if err := json.Unmarshal(out.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Id != 42 || body.TripId != tripID || body.Coordinates.Latitude != point.Latitude ||
				body.Coordinates.Longitude != point.Longitude || !body.RecordedAt.Equal(recordedAt) ||
				out.Header().Get("Content-Type") != jsonContentType {
				t.Fatalf("unexpected position response: %s", out.Body.String())
			}
		})
	}
}

func TestHandlerRejectsMalformedRequest(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
	}{
		{"content type", "text/plain", validBody},
		{"missing coordinates", jsonContentType, `{"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"missing latitude", jsonContentType, `{"coordinates":{"longitude":30.3},"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"null longitude", jsonContentType, `{"coordinates":{"latitude":59.9,"longitude":null},"recorded_at":"2026-08-24T12:00:05Z"}`},
		{"missing recorded_at", jsonContentType, `{"coordinates":{"latitude":59.9,"longitude":30.3}}`},
		{"unparsable recorded_at", jsonContentType, `{"coordinates":{"latitude":59.9,"longitude":30.3},"recorded_at":"yesterday"}`},
		{"unknown field", jsonContentType, `{"coordinates":{"latitude":59.9,"longitude":30.3},"recorded_at":"2026-08-24T12:00:05Z","speed":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usecase := NewMockusecase(gomock.NewController(t))
			req := httptest.NewRequest("POST", "/api/v1/trips/x/positions", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", tc.contentType)
			out := httptest.NewRecorder()
			create.New(usecase, response.New()).Handle(out, req, uuid.New())
			var problem api.Problem
			if out.Code != 400 || json.Unmarshal(out.Body.Bytes(), &problem) != nil || problem.Code != "invalid_request" {
				t.Fatalf("status %d, body %s", out.Code, out.Body.String())
			}
		})
	}
}
