package list

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

type Usecase struct {
	repo positionRepository
}

func New(repo positionRepository) *Usecase {
	return &Usecase{repo: repo}
}

func (u *Usecase) List(ctx context.Context, tripID uuid.UUID) ([]trip.Position, error) {
	if _, err := u.repo.Get(ctx, tripID); err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	positions, err := u.repo.ListPositions(ctx, tripID)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	return positions, nil
}
