package list_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/usecase/position/list"
)

func TestList(t *testing.T) {
	storageErr := errors.New("storage failure")
	tripID := uuid.New()
	positions := []trip.Position{{ID: 1, TripID: tripID}, {ID: 2, TripID: tripID}}
	cases := []struct {
		name            string
		getErr, listErr error
		want            []trip.Position
	}{
		{name: "success", want: positions},
		{name: "empty route", want: []trip.Position{}},
		{name: "trip not found", getErr: trip.ErrNotFound},
		{name: "read trip failure", getErr: storageErr},
		{name: "list failure", listErr: storageErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := NewMockpositionRepository(gomock.NewController(t))
			ctx := t.Context()
			get := repo.EXPECT().Get(ctx, tripID).Return(trip.Trip{ID: tripID}, tc.getErr)
			if tc.getErr == nil {
				repo.EXPECT().ListPositions(ctx, tripID).After(get).Return(tc.want, tc.listErr)
			}
			got, err := list.New(repo).List(ctx, tripID)
			wantErr := tc.getErr
			if wantErr == nil {
				wantErr = tc.listErr
			}
			if !errors.Is(err, wantErr) {
				t.Fatalf("List: got %v, want %v", err, wantErr)
			}
			if wantErr == nil && !slices.Equal(got, tc.want) {
				t.Fatalf("List: got %+v, want %+v", got, tc.want)
			}
		})
	}
}
