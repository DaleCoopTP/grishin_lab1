package trip

import (
	"context"
	"fmt"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/google/uuid"

	tripdomain "github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
)

const lastSeenExpr = "COALESCE(last_position_at, started_at)"

func (r *Repository) InsertPosition(ctx context.Context, position tripdomain.Position) (int64, error) {
	query, args, err := squirrel.Insert("trip_positions").
		Columns("trip_id", "latitude", "longitude", "recorded_at").
		Values(position.TripID, position.Point.Latitude, position.Point.Longitude, position.RecordedAt).
		Suffix("RETURNING id").
		PlaceholderFormat(squirrel.Dollar).
		ToSql()
	if err != nil {
		return 0, fmt.Errorf("build insert position: %w", err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	var id int64
	if err := r.tx.Executor(queryCtx).QueryRow(queryCtx, query, args...).Scan(&id); err != nil {
		return 0, fmt.Errorf("insert position (SQL %q): %w", query, err)
	}
	return id, nil
}

func (r *Repository) TouchLastPosition(ctx context.Context, tripID uuid.UUID, recordedAt time.Time) error {
	query, args, err := squirrel.Update("trips").
		Set("last_position_at", squirrel.Expr("GREATEST(last_position_at, ?::timestamptz)", recordedAt)).
		Set("updated_at", squirrel.Expr("now()")).
		Where(squirrel.Eq{"id": tripID}).
		PlaceholderFormat(squirrel.Dollar).
		ToSql()
	if err != nil {
		return fmt.Errorf("build touch last position: %w", err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	result, err := r.tx.Executor(queryCtx).Exec(queryCtx, query, args...)
	if err != nil {
		return fmt.Errorf("touch last position (SQL %q): %w", query, err)
	}
	if result.RowsAffected() != 1 {
		return fmt.Errorf("touch last position (SQL %q): expected one row, got %d", query, result.RowsAffected())
	}
	return nil
}

func (r *Repository) ListPositions(ctx context.Context, tripID uuid.UUID) ([]tripdomain.Position, error) {
	query, args, err := squirrel.Select("id", "trip_id", "latitude", "longitude", "recorded_at").
		From("trip_positions").
		Where(squirrel.Eq{"trip_id": tripID}).
		OrderBy("recorded_at", "id").
		PlaceholderFormat(squirrel.Dollar).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list positions: %w", err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	rows, err := r.tx.Executor(queryCtx).Query(queryCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list positions (SQL %q): %w", query, err)
	}
	defer rows.Close()
	positions := []tripdomain.Position{}
	for rows.Next() {
		var position tripdomain.Position
		if err := rows.Scan(&position.ID, &position.TripID, &position.Point.Latitude, &position.Point.Longitude, &position.RecordedAt); err != nil {
			return nil, fmt.Errorf("scan position (SQL %q): %w", query, err)
		}
		positions = append(positions, position)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list positions (SQL %q): %w", query, err)
	}
	return positions, nil
}

// ListStale returns up to limit active trips silent for longer than staleAfter,
// the longest silent first. Pass the last trip of the previous batch as after
// to continue: the next batch starts strictly after its (last_seen_at, id) key,
// so no OFFSET scan is needed and the query stays on the index.
func (r *Repository) ListStale(ctx context.Context, staleAfter time.Duration, after *tripdomain.StaleTrip, limit int) ([]tripdomain.StaleTrip, error) {
	builder := squirrel.Select("id", "driver_id", lastSeenExpr+" AS last_seen_at").
		From("trips").
		Where("status = ?", tripdomain.StatusActive).
		Where(lastSeenExpr+" < now() - ?::interval", staleAfter)
	if after != nil {
		builder = builder.Where("("+lastSeenExpr+", id) > (?::timestamptz, ?::uuid)", after.LastSeenAt, after.ID)
	}
	query, args, err := builder.
		OrderBy(lastSeenExpr, "id").
		Limit(uint64(max(limit, 1))).
		PlaceholderFormat(squirrel.Dollar).
		ToSql()
	if err != nil {
		return nil, fmt.Errorf("build list stale trips: %w", err)
	}
	queryCtx, cancel := context.WithTimeout(ctx, r.queryTimeout)
	defer cancel()
	rows, err := r.tx.Executor(queryCtx).Query(queryCtx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list stale trips (SQL %q): %w", query, err)
	}
	defer rows.Close()
	trips := make([]tripdomain.StaleTrip, 0, limit)
	for rows.Next() {
		var trip tripdomain.StaleTrip
		if err := rows.Scan(&trip.ID, &trip.DriverID, &trip.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan stale trip (SQL %q): %w", query, err)
		}
		trips = append(trips, trip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list stale trips (SQL %q): %w", query, err)
	}
	return trips, nil
}
