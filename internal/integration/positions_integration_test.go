//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	tripdomain "github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/generated"
	"github.com/DaleCoopTP/grishin_lab1/internal/infra/postgres/transaction"
	"github.com/DaleCoopTP/grishin_lab1/internal/model"
	triprepository "github.com/DaleCoopTP/grishin_lab1/internal/repository/trip"
	positionsave "github.com/DaleCoopTP/grishin_lab1/internal/usecase/position/save"
)

type recordingPending struct{ cancelled []uuid.UUID }

func (p *recordingPending) Cancel(tripID uuid.UUID) { p.cancelled = append(p.cancelled, tripID) }

func positionRepository(pool *pgxpool.Pool) (*triprepository.Repository, *transaction.Manager) {
	tx := transaction.NewTxManager(pool, 5*time.Second)
	return triprepository.NewRepository(tx, 3*time.Second), tx
}

func createActiveTrip(t *testing.T, ctx context.Context, usecases tripUsecases, pool *pgxpool.Pool) tripdomain.Trip {
	t.Helper()
	driverID := uuid.New()
	cleanupDriver(t, pool, driverID)
	trip, err := usecases.create.Create(ctx, model.CreateTripInput{
		UserID: uuid.New(), DriverID: driverID,
		StartPoint: tripdomain.Point{Latitude: 59.9398, Longitude: 30.3146},
		EndPoint:   tripdomain.Point{Latitude: 59.929, Longitude: 30.3626},
		Price:      1450,
	})
	if err != nil {
		t.Fatal(err)
	}
	return trip
}

func lastPositionAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tripID uuid.UUID) *time.Time {
	t.Helper()
	var value *time.Time
	if err := pool.QueryRow(ctx, "SELECT last_position_at FROM trips WHERE id = $1", tripID).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSavePositionUpdatesLastPositionAt(t *testing.T) {
	usecases, pool := testUsecases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, tx := positionRepository(pool)
	pending := &recordingPending{}
	save := positionsave.New(repo, tx, pending, nil)
	trip := createActiveTrip(t, ctx, usecases, pool)

	fresh := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	late := fresh.Add(-10 * time.Minute)
	point := tripdomain.Point{Latitude: 59.935, Longitude: 30.335}
	first, err := save.Save(ctx, trip.ID, point, fresh)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || first.TripID != trip.ID || !first.RecordedAt.Equal(fresh) {
		t.Fatalf("saved position: %+v", first)
	}
	if got := lastPositionAt(t, ctx, pool, trip.ID); got == nil || !got.Equal(fresh) {
		t.Fatalf("last_position_at after fresh point: got %v, want %s", got, fresh)
	}

	if _, err := save.Save(ctx, trip.ID, point, late); err != nil {
		t.Fatal(err)
	}
	if got := lastPositionAt(t, ctx, pool, trip.ID); got == nil || !got.Equal(fresh) {
		t.Fatalf("late point aged the trip: got %v, want %s", got, fresh)
	}
	if len(pending.cancelled) != 2 || pending.cancelled[0] != trip.ID {
		t.Fatalf("pending requests were not cancelled after commit: %v", pending.cancelled)
	}

	route, err := repo.ListPositions(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(route) != 2 || !route[0].RecordedAt.Equal(late) || !route[1].RecordedAt.Equal(fresh) {
		t.Fatalf("route is not ordered by recorded_at: %+v", route)
	}

	if _, err := usecases.finish.Finish(ctx, trip.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := save.Save(ctx, trip.ID, point, fresh); !errors.Is(err, tripdomain.ErrCompleted) {
		t.Fatalf("save into completed trip: got %v, want ErrCompleted", err)
	}
	if _, err := save.Save(ctx, uuid.New(), point, fresh); !errors.Is(err, tripdomain.ErrNotFound) {
		t.Fatalf("save into missing trip: got %v, want ErrNotFound", err)
	}
}

func TestSavePositionRollsBackWhenTripUpdateFails(t *testing.T) {
	usecases, pool := testUsecases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, tx := positionRepository(pool)
	pending := &recordingPending{}
	save := positionsave.New(repo, tx, pending, nil)
	trip := createActiveTrip(t, ctx, usecases, pool)

	name := "trip_position_touch_fail_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	functionName := name + "_fn"
	functionSQL := fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.id = '%s' AND NEW.last_position_at IS DISTINCT FROM OLD.last_position_at THEN
        RAISE EXCEPTION 'forced last position failure';
    END IF;
    RETURN NEW;
END $$`, functionName, trip.ID)
	if _, err := pool.Exec(ctx, functionSQL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := pool.Exec(cleanupCtx, "DROP TRIGGER IF EXISTS "+name+" ON trips"); err != nil {
			t.Errorf("drop trigger: %v", err)
		}
		if _, err := pool.Exec(cleanupCtx, "DROP FUNCTION "+functionName+"()"); err != nil {
			t.Errorf("drop trigger function: %v", err)
		}
	})
	if _, err := pool.Exec(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE UPDATE ON trips FOR EACH ROW EXECUTE FUNCTION %s()", name, functionName)); err != nil {
		t.Fatal(err)
	}

	_, err := save.Save(ctx, trip.ID, tripdomain.Point{Latitude: 1, Longitude: 2}, time.Now().Add(-time.Second))
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Message != "forced last position failure" {
		t.Fatalf("expected forced update failure, got %v", err)
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM trip_positions WHERE trip_id = $1", trip.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("position survived rollback: count %d, error %v", count, err)
	}
	if got := lastPositionAt(t, ctx, pool, trip.ID); got != nil {
		t.Fatalf("last_position_at changed despite rollback: %s", got)
	}
	if len(pending.cancelled) != 0 {
		t.Fatalf("pending request cancelled for a rolled back position: %v", pending.cancelled)
	}
}

func TestListStaleSelectsOnlySilentActiveTrips(t *testing.T) {
	usecases, pool := testUsecases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	repo, _ := positionRepository(pool)
	age := func(trip tripdomain.Trip, started, lastPosition string) {
		t.Helper()
		if _, err := pool.Exec(ctx, "UPDATE trips SET started_at = now() - $2::interval, last_position_at = now() - $3::interval WHERE id = $1",
			trip.ID, started, lastPosition); err != nil {
			t.Fatal(err)
		}
	}
	silentLongest := createActiveTrip(t, ctx, usecases, pool)
	if _, err := pool.Exec(ctx, "UPDATE trips SET started_at = now() - interval '20 minutes' WHERE id = $1", silentLongest.ID); err != nil {
		t.Fatal(err)
	}
	silent := createActiveTrip(t, ctx, usecases, pool)
	age(silent, "30 minutes", "5 minutes")
	fresh := createActiveTrip(t, ctx, usecases, pool)
	age(fresh, "30 minutes", "10 seconds")
	justStarted := createActiveTrip(t, ctx, usecases, pool)
	completed := createActiveTrip(t, ctx, usecases, pool)
	if _, err := usecases.finish.Finish(ctx, completed.ID); err != nil {
		t.Fatal(err)
	}
	age(completed, "40 minutes", "40 minutes")

	stale, err := repo.ListStale(ctx, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	position := map[uuid.UUID]int{}
	for i, trip := range stale {
		position[trip.ID] = i
		if i > 0 && stale[i-1].LastSeenAt.After(trip.LastSeenAt) {
			t.Fatalf("stale trips are not ordered by silence: %+v", stale)
		}
	}
	for name, trip := range map[string]tripdomain.Trip{"fresh": fresh, "just started": justStarted, "completed": completed} {
		if _, ok := position[trip.ID]; ok {
			t.Fatalf("%s trip must not be polled", name)
		}
	}
	longest, ok := position[silentLongest.ID]
	if !ok {
		t.Fatal("trip without positions must be polled")
	}
	other, ok := position[silent.ID]
	if !ok {
		t.Fatal("trip with an old position must be polled")
	}
	if longest > other || stale[longest].DriverID != silentLongest.DriverID {
		t.Fatalf("trip silent for the longest time must come first: %+v", stale)
	}
}

func TestListStaleUsesIndex(t *testing.T) {
	_, pool := testUsecases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `INSERT INTO trips (id, user_id, driver_id, start_latitude, start_longitude, end_latitude, end_longitude,
		price, status, started_at, finished_at)
	SELECT gen_random_uuid(), gen_random_uuid(), gen_random_uuid(), 1, 1, 2, 2, 100,
		CASE WHEN g % 20 = 0 THEN 'active' ELSE 'completed' END,
		now() - make_interval(secs => g),
		CASE WHEN g % 20 = 0 THEN NULL ELSE now() END
	FROM generate_series(1, 20000) AS g`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "ANALYZE trips"); err != nil {
		t.Fatal(err)
	}
	var plan string
	err = tx.QueryRow(ctx, `EXPLAIN (COSTS OFF)
		SELECT id, driver_id, COALESCE(last_position_at, started_at) AS last_seen_at FROM trips
		WHERE status = 'active' AND COALESCE(last_position_at, started_at) < now() - interval '1 minute'
		ORDER BY COALESCE(last_position_at, started_at), id`, pgx.QueryExecModeSimpleProtocol).Scan(&plan)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "trips_active_last_seen_idx") || strings.Contains(plan, "Seq Scan") {
		t.Fatalf("stale trips query does not use its index: %s", plan)
	}
}

func TestPositionHTTPContract(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Fatal("TEST_DATABASE_URL is not set")
	}
	usecases, pool := testUsecases(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	router := testRouter(pool)
	trip := createActiveTrip(t, ctx, usecases, pool)
	path := "/api/v1/trips/" + trip.ID.String() + "/positions"
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", jsonContentType)
		}
		out := httptest.NewRecorder()
		router.ServeHTTP(out, req)
		return out
	}
	body := func(latitude float64, recordedAt string) string {
		return fmt.Sprintf(`{"coordinates":{"latitude":%v,"longitude":30.335},"recorded_at":%q}`, latitude, recordedAt)
	}
	now := time.Now().UTC().Truncate(time.Second)
	later, earlier := now.Add(-time.Minute), now.Add(-2*time.Minute)

	empty := request(http.MethodGet, path, "")
	if empty.Code != http.StatusOK || strings.TrimSpace(empty.Body.String()) != `{"positions":[]}` {
		t.Fatalf("empty route: status %d, body %s", empty.Code, empty.Body.String())
	}
	times := []time.Time{later, earlier, earlier}
	saved := make([]api.TripPosition, 0, len(times))
	for _, at := range times {
		created := request(http.MethodPost, path, body(59.935, at.Format(time.RFC3339)))
		var position api.TripPosition
		if created.Code != http.StatusCreated || created.Header().Get("Content-Type") != jsonContentType ||
			json.Unmarshal(created.Body.Bytes(), &position) != nil || position.Id == 0 || position.TripId != trip.ID ||
			!position.RecordedAt.Equal(at) {
			t.Fatalf("create position: status %d, body %s", created.Code, created.Body.String())
		}
		saved = append(saved, position)
	}
	for _, invalid := range []string{
		body(91, now.Format(time.RFC3339)),
		body(-90.5, now.Format(time.RFC3339)),
		body(59.9, now.Add(2*time.Minute).Format(time.RFC3339)),
		body(59.9, "24.08.2026 12:00"),
		`{"coordinates":{"latitude":59.9},"recorded_at":"2026-08-24T12:00:05Z"}`,
	} {
		assertProblem(t, request(http.MethodPost, path, invalid), http.StatusBadRequest, "invalid_request")
	}
	assertProblem(t, request(http.MethodPost, "/api/v1/trips/not-a-uuid/positions", body(1, now.Format(time.RFC3339))), http.StatusBadRequest, "invalid_request")

	listed := request(http.MethodGet, path, "")
	var route api.TripPositionsResponse
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &route) != nil || len(route.Positions) != 3 {
		t.Fatalf("route: status %d, body %s", listed.Code, listed.Body.String())
	}
	wantOrder := []int64{saved[1].Id, saved[2].Id, saved[0].Id}
	for i, position := range route.Positions {
		if position.Id != wantOrder[i] {
			t.Fatalf("route order by recorded_at, id: got %s", listed.Body.String())
		}
	}

	var tripBody api.Trip
	got := request(http.MethodGet, "/api/v1/trips/"+trip.ID.String(), "")
	if json.Unmarshal(got.Body.Bytes(), &tripBody) != nil || tripBody.LastPositionAt == nil || !tripBody.LastPositionAt.Equal(later) {
		t.Fatalf("trip last_position_at: %s", got.Body.String())
	}

	missing := "/api/v1/trips/" + uuid.NewString() + "/positions"
	assertProblem(t, request(http.MethodPost, missing, body(59.9, now.Format(time.RFC3339))), http.StatusNotFound, "trip_not_found")
	assertProblem(t, request(http.MethodGet, missing, ""), http.StatusNotFound, "trip_not_found")
	if finished := request(http.MethodPost, "/api/v1/trips/"+trip.ID.String()+"/finish", ""); finished.Code != http.StatusOK {
		t.Fatalf("finish: status %d", finished.Code)
	}
	assertProblem(t, request(http.MethodPost, path, body(59.9, now.Format(time.RFC3339))), http.StatusConflict, "trip_completed")
	if completedRoute := request(http.MethodGet, path, ""); completedRoute.Code != http.StatusOK {
		t.Fatalf("route of completed trip: status %d", completedRoute.Code)
	}
}
