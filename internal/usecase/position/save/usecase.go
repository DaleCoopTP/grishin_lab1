package save

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type Usecase struct {
	repo    positionRepository
	tx      txManager
	pending pendingRequests
	events  positionEvents
}

func New(repo positionRepository, tx txManager, pending pendingRequests, events positionEvents) *Usecase {
	return &Usecase{repo: repo, tx: tx, pending: pending, events: events}
}

func (u *Usecase) Save(ctx context.Context, tripID uuid.UUID, point trip.Point, recordedAt time.Time) (trip.Position, error) {
	position, err := trip.NewPosition(tripID, point, recordedAt, time.Now())
	if err != nil {
		return trip.Position{}, err
	}
	if err := u.tx.Do(ctx, func(txCtx context.Context) error {
		current, err := u.repo.GetForUpdate(txCtx, tripID)
		if err != nil {
			return err
		}
		if current.Status != trip.StatusActive {
			return trip.ErrCompleted
		}
		if position.ID, err = u.repo.InsertPosition(txCtx, position); err != nil {
			return err
		}
		return u.repo.TouchLastPosition(txCtx, tripID, position.RecordedAt)
	}); err != nil {
		return trip.Position{}, fmt.Errorf("save position: %w", err)
	}
	if u.pending != nil {
		u.pending.Cancel(tripID)
	}
	if u.events != nil {
		u.events.Saved(ctx, position)
	}
	return position, nil
}
