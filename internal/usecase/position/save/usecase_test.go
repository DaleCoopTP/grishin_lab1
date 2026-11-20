package save_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/usecase/position/save"
)

func TestSave(t *testing.T) {
	storageErr := errors.New("storage failure")
	commitErr := errors.New("commit failure")
	cases := []struct {
		name                                   string
		status                                 string
		getErr, insertErr, touchErr, commitErr error
		wantErr                                error
	}{
		{name: "success", status: trip.StatusActive},
		{name: "not found", getErr: trip.ErrNotFound, wantErr: trip.ErrNotFound},
		{name: "completed", status: trip.StatusCompleted, wantErr: trip.ErrCompleted},
		{name: "read failure", getErr: storageErr, wantErr: storageErr},
		{name: "insert failure", status: trip.StatusActive, insertErr: storageErr, wantErr: storageErr},
		{name: "touch failure", status: trip.StatusActive, touchErr: storageErr, wantErr: storageErr},
		{name: "commit failure", status: trip.StatusActive, commitErr: commitErr, wantErr: commitErr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			repo, tx := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl)
			pending, events := NewMockpendingRequests(ctrl), NewMockpositionEvents(ctrl)
			ctx := t.Context()
			txCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			committed := false
			tx.EXPECT().Do(ctx, gomock.Any()).DoAndReturn(func(_ context.Context, fn func(context.Context) error) error {
				if err := fn(txCtx); err != nil {
					return err
				}
				committed = tc.commitErr == nil
				return tc.commitErr
			})
			tripID := uuid.New()
			point := trip.Point{Latitude: 59.935, Longitude: 30.335}
			recordedAt := time.Now().Add(-time.Minute).UTC().Truncate(time.Microsecond)
			load := repo.EXPECT().GetForUpdate(txCtx, tripID).Return(trip.Trip{ID: tripID, Status: tc.status}, tc.getErr)
			if tc.getErr == nil && tc.status == trip.StatusActive {
				insert := repo.EXPECT().InsertPosition(txCtx, trip.Position{TripID: tripID, Point: point, RecordedAt: recordedAt}).
					After(load).Return(int64(42), tc.insertErr)
				if tc.insertErr == nil {
					repo.EXPECT().TouchLastPosition(txCtx, tripID, recordedAt).After(insert).Return(tc.touchErr)
				}
			}
			want := trip.Position{ID: 42, TripID: tripID, Point: point, RecordedAt: recordedAt}
			if tc.wantErr == nil {
				cancelPending := pending.EXPECT().Cancel(tripID).Do(func(uuid.UUID) {
					if !committed {
						t.Fatal("pending request cancelled before commit")
					}
				})
				events.EXPECT().Saved(ctx, want).After(cancelPending)
			}

			got, err := save.New(repo, tx, pending, events).Save(ctx, tripID, point, recordedAt)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Save: got %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				want = trip.Position{}
			}
			if got != want {
				t.Fatalf("Save: got %+v, want %+v", got, want)
			}
		})
	}
}

func TestSaveRejectsInvalidPositionBeforeTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	usecase := save.New(NewMockpositionRepository(ctrl), NewMocktxManager(ctrl), NewMockpendingRequests(ctrl), NewMockpositionEvents(ctrl))
	for _, tc := range []struct {
		name       string
		point      trip.Point
		recordedAt time.Time
	}{
		{"latitude", trip.Point{Latitude: 91}, time.Now()},
		{"future", trip.Point{}, time.Now().Add(2 * time.Minute)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := usecase.Save(t.Context(), uuid.New(), tc.point, tc.recordedAt)
			var validationError *trip.ValidationError
			if !errors.As(err, &validationError) {
				t.Fatalf("got %v, want validation error", err)
			}
		})
	}
}

func TestSaveWithoutOptionalDependencies(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo, tx := NewMockpositionRepository(ctrl), NewMocktxManager(ctrl)
	tx.EXPECT().Do(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) })
	repo.EXPECT().GetForUpdate(gomock.Any(), gomock.Any()).Return(trip.Trip{Status: trip.StatusActive}, nil)
	repo.EXPECT().InsertPosition(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	repo.EXPECT().TouchLastPosition(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	if _, err := save.New(repo, tx, nil, nil).Save(t.Context(), uuid.New(), trip.Point{}, time.Now()); err != nil {
		t.Fatal(err)
	}
}
