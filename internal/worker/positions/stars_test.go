package positions_test

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/worker/positions"
)

func compareKey(a, b trip.StaleTrip) int {
	if c := a.LastSeenAt.Compare(b.LastSeenAt); c != 0 {
		return c
	}
	return bytes.Compare(a.ID[:], b.ID[:])
}

type keysetReader struct {
	mu     sync.Mutex
	passes [][]trip.StaleTrip
	table  []trip.StaleTrip
	calls  []*trip.StaleTrip
}

func newKeysetReader(ctrl *gomock.Controller, passes ...[]trip.StaleTrip) (*MockstaleReader, *keysetReader) {
	k := &keysetReader{}
	for _, table := range passes {
		sorted := slices.Clone(table)
		slices.SortFunc(sorted, compareKey)
		k.passes = append(k.passes, sorted)
	}
	reader := NewMockstaleReader(ctrl)
	reader.EXPECT().ListStale(gomock.Any(), staleAfter, gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _ time.Duration, after *trip.StaleTrip, limit int) ([]trip.StaleTrip, error) {
			k.mu.Lock()
			defer k.mu.Unlock()
			k.calls = append(k.calls, after)
			start := 0
			if after == nil {
				k.table = nil
				if len(k.passes) > 0 {
					k.table, k.passes = k.passes[0], k.passes[1:]
				}
			} else {
				start, _ = slices.BinarySearchFunc(k.table, *after, compareKey)
				if start < len(k.table) && compareKey(k.table[start], *after) == 0 {
					start++
				}
			}
			end := min(start+limit, len(k.table))
			return slices.Clone(k.table[start:end]), nil
		}).AnyTimes()
	return reader, k
}

func (k *keysetReader) recordedCalls() []*trip.StaleTrip {
	k.mu.Lock()
	defer k.mu.Unlock()
	return slices.Clone(k.calls)
}

func tripsWithTies(n int) []trip.StaleTrip {
	base := time.Now().Add(-time.Hour).Truncate(time.Microsecond)
	trips := make([]trip.StaleTrip, n)
	for i := range trips {
		trips[i] = trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: base.Add(time.Duration(i/5) * time.Second)}
	}
	return trips
}

func TestScanReadsAllBatchesWithKeyset(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := tripsWithTies(250)
	reader, k := newKeysetReader(ctrl, trips)
	var mu sync.Mutex
	pushed := map[uuid.UUID]int{}
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, _, id uuid.UUID) error {
			mu.Lock()
			defer mu.Unlock()
			pushed[id]++
			return nil
		}).Times(len(trips))
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 300), reader, push, m)

	h.ticker.tick(t)
	h.ticker.tick(t)
	calls := k.recordedCalls()
	if len(calls) < 3 || calls[0] != nil || calls[1] == nil || calls[2] == nil {
		t.Fatalf("first tick must read three batches starting without a cursor, got %v", calls)
	}
	if got := c.rowsPerScan(); c.staleChecks(positions.ResultQueued) != len(trips) || got[0] != len(trips) {
		t.Fatalf("queued %d, rows read per tick %v", c.staleChecks(positions.ResultQueued), got)
	}
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, trip := range trips {
		if pushed[trip.ID] != 1 {
			t.Fatalf("trip %s pushed %d times", trip.ID, pushed[trip.ID])
		}
	}
}

func TestScanStopsAfterOverflowBatchAndRestarts(t *testing.T) {
	ctrl := gomock.NewController(t)
	busy := staleTrips(1)[0]
	trips := tripsWithTies(250)
	reader, k := newKeysetReader(ctrl, []trip.StaleTrip{busy}, trips, trips)
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
	push.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 150), reader, push, m)

	h.ticker.tick(t)
	g.awaitStart(t, busy.ID)
	h.ticker.tick(t)
	h.ticker.tick(t)
	h.ticker.tick(t)

	calls := k.recordedCalls()
	wantCursor := []bool{false, false, true, false, true}
	if len(calls) < len(wantCursor) {
		t.Fatalf("got %d calls, want at least %d", len(calls), len(wantCursor))
	}
	for i, cursor := range wantCursor {
		if (calls[i] != nil) != cursor {
			t.Fatalf("each overflowing tick must read two batches and the next one must restart: %v", calls)
		}
	}
	if got := c.rowsPerScan(); got[1] != 200 || got[2] != 200 {
		t.Fatalf("rows read per tick: %v", got)
	}
	if c.staleChecks(positions.ResultQueued) != 1+150 || c.staleChecks(positions.ResultDuplicate) != 150 ||
		c.staleChecks(positions.ResultDropped) != 100 {
		t.Fatalf("stale checks: %v", c.stale)
	}
	g.open()
}

func TestWorkerTakesLongestSilentTripFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	busy := staleTrips(1)[0]
	now := time.Now()
	recent := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-10 * time.Minute)}
	oldest := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-30 * time.Minute)}
	middle := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-20 * time.Minute)}
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	gomock.InOrder(
		push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
			func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) }),
		push.EXPECT().RequestPosition(gomock.Any(), oldest.DriverID, oldest.ID).Return(nil),
		push.EXPECT().RequestPosition(gomock.Any(), middle.DriverID, middle.ID).Return(nil),
		push.EXPECT().RequestPosition(gomock.Any(), recent.DriverID, recent.ID).Return(nil),
	)
	m, _ := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(busy), found(recent, oldest, middle)), push, m)

	h.ticker.tick(t)
	g.awaitStart(t, busy.ID)
	h.ticker.tick(t)
	h.ticker.tick(t)
	g.open()
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestFullQueueEvictsFreshestTrip(t *testing.T) {
	ctrl := gomock.NewController(t)
	busy := staleTrips(1)[0]
	now := time.Now()
	freshest := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-5 * time.Minute)}
	fresh := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-6 * time.Minute)}
	old := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-time.Hour)}
	newest := trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: now.Add(-time.Minute)}
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	gomock.InOrder(
		push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
			func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) }),
		push.EXPECT().RequestPosition(gomock.Any(), old.DriverID, old.ID).Return(nil),
		push.EXPECT().RequestPosition(gomock.Any(), fresh.DriverID, fresh.ID).Return(nil),
	)
	m, c := recordingMetrics(ctrl)
	reader := scriptedReader(ctrl, found(busy), found(freshest, fresh), found(old), found(newest))
	h := start(t, context.Background(), config(1, 2), reader, push, m)

	h.ticker.tick(t)
	g.awaitStart(t, busy.ID)
	for range 4 {
		h.ticker.tick(t)
	}
	if c.staleChecks(positions.ResultQueued) != 4 || c.staleChecks(positions.ResultDropped) != 1 ||
		c.requests(positions.ResultDropped) != 2 || h.svc.QueueSize() != 2 {
		t.Fatalf("stale checks %v, dropped requests %d, queue %d", c.stale, c.dropped, h.svc.QueueSize())
	}
	g.open()
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
