package positions_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/worker/positions"
)

func TestTickDoesNotQueueTripTwice(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := staleTrips(2)
	busy, waiting := trips[0], trips[1]
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
	push.EXPECT().RequestPosition(gomock.Any(), waiting.DriverID, waiting.ID).Return(nil)
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(busy, waiting), found(waiting, busy)), push, m)

	h.ticker.tick(t)
	g.awaitStart(t, busy.ID)
	h.ticker.tick(t)
	h.ticker.tick(t)

	if c.staleChecks(positions.ResultQueued) != 2 || c.staleChecks(positions.ResultDuplicate) != 2 {
		t.Fatalf("stale checks: %v", c.stale)
	}
	g.open()
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.requests(positions.ResultSuccess) != 2 {
		t.Fatalf("each trip must be requested once: %v", c.finished)
	}
}

func TestPositionCancelsQueuedRequest(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		trips := staleTrips(2)
		busy, cancelled := trips[0], trips[1]
		g := newGate()
		defer g.open()
		push := NewMockpusher(ctrl)
		push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
			func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
		m, c := recordingMetrics(ctrl)
		h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(busy, cancelled)), push, m)

		h.ticker.tick(t)
		g.awaitStart(t, busy.ID)
		h.svc.Cancel(cancelled.ID)
		g.open()
		if err := h.svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c.requests(positions.ResultSuccess) != 1 {
			t.Fatalf("cancelled trip was requested: %v", c.finished)
		}
	})
	t.Run("queued again after cancel", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		trips := staleTrips(2)
		busy, again := trips[0], trips[1]
		g := newGate()
		defer g.open()
		push := NewMockpusher(ctrl)
		push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
			func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
		push.EXPECT().RequestPosition(gomock.Any(), again.DriverID, again.ID).Return(nil).Times(1)
		m, c := recordingMetrics(ctrl)
		h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(busy, again), found(again)), push, m)

		h.ticker.tick(t)
		g.awaitStart(t, busy.ID)
		h.svc.Cancel(again.ID)
		h.ticker.tick(t)
		h.ticker.tick(t)
		if c.staleChecks(positions.ResultQueued) != 3 || c.staleChecks(positions.ResultDuplicate) != 0 {
			t.Fatalf("stale checks: %v", c.stale)
		}
		g.open()
		if err := h.svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestQueueOverflowDropsInsteadOfBlocking(t *testing.T) {
	ctrl := gomock.NewController(t)
	busy := staleTrips(1)[0]
	burst := staleTrips(5)
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), busy.DriverID, busy.ID).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
	for _, trip := range burst[:2] {
		push.EXPECT().RequestPosition(gomock.Any(), trip.DriverID, trip.ID).Return(nil)
	}
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 2), scriptedReader(ctrl, found(busy), found(burst...), found(burst...)), push, m)

	h.ticker.tick(t)
	g.awaitStart(t, busy.ID)
	h.ticker.tick(t)
	h.ticker.tick(t)
	h.ticker.tick(t)

	if c.staleChecks(positions.ResultQueued) != 3 || c.staleChecks(positions.ResultDropped) != 6 ||
		c.staleChecks(positions.ResultDuplicate) != 2 || c.requests(positions.ResultDropped) != 6 {
		t.Fatalf("stale checks %v, dropped requests %d", c.stale, c.dropped)
	}
	if h.svc.QueueSize() != 2 {
		t.Fatalf("queue size %d, want 2", h.svc.QueueSize())
	}
	g.open()
}

func TestPoolProcessesTasksWithFixedWorkers(t *testing.T) {
	const workers, tasks = 3, 10
	ctrl := gomock.NewController(t)
	trips := staleTrips(tasks)
	g := newGate()
	defer g.open()
	var current, peak atomic.Int64
	pushed := make(map[uuid.UUID]int)
	pushedCh := make(chan uuid.UUID, tasks)
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error {
			now := current.Add(1)
			defer current.Add(-1)
			for {
				old := peak.Load()
				if now <= old || peak.CompareAndSwap(old, now) {
					break
				}
			}
			pushedCh <- id
			return g.wait(ctx, id)
		}).Times(tasks)
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(workers, tasks), scriptedReader(ctrl, found(trips...)), push, m)

	h.ticker.tick(t)
	for range workers {
		<-g.started
	}
	h.ticker.tick(t)
	if h.svc.ActiveWorkers() != workers || h.svc.QueueSize() != tasks-workers {
		t.Fatalf("active workers %d, queue %d", h.svc.ActiveWorkers(), h.svc.QueueSize())
	}
	g.open()
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(pushedCh)
	for id := range pushedCh {
		pushed[id]++
	}
	for _, trip := range trips {
		if pushed[trip.ID] != 1 {
			t.Fatalf("trip %s pushed %d times", trip.ID, pushed[trip.ID])
		}
	}
	if peak.Load() != workers || c.requests(positions.ResultSuccess) != tasks || h.svc.ActiveWorkers() != 0 {
		t.Fatalf("peak concurrency %d, successes %d", peak.Load(), c.requests(positions.ResultSuccess))
	}
}

func TestContextCancelStopsScannerAndWorkers(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctrl := gomock.NewController(t)
	trips := staleTrips(3)
	g := newGate()
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) }).Times(2)
	m, c := recordingMetrics(ctrl)
	ctx, cancel := context.WithCancel(context.Background())
	h := start(t, ctx, config(2, 10), scriptedReader(ctrl, found(trips...)), push, m)

	h.ticker.tick(t)
	<-g.started
	<-g.started
	cancel()
	<-h.ticker.stopped
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.requests(positions.ResultError) != 2 {
		t.Fatalf("in-flight requests must be cancelled: %v", c.finished)
	}
}

func TestPushFailureDoesNotStopWorker(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := staleTrips(3)
	push := NewMockpusher(ctrl)
	gomock.InOrder(
		push.EXPECT().RequestPosition(gomock.Any(), trips[0].DriverID, trips[0].ID).Return(errors.New("503 from push service")),
		push.EXPECT().RequestPosition(gomock.Any(), trips[1].DriverID, trips[1].ID).DoAndReturn(
			func(context.Context, uuid.UUID, uuid.UUID) error { panic("broken client") }),
		push.EXPECT().RequestPosition(gomock.Any(), trips[2].DriverID, trips[2].ID).Return(nil),
	)
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(trips...)), push, m)

	h.ticker.tick(t)
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.requests(positions.ResultError) != 2 || c.requests(positions.ResultSuccess) != 1 {
		t.Fatalf("request results: %v", c.finished)
	}
}

func TestScanFailureDoesNotStopScanner(t *testing.T) {
	ctrl := gomock.NewController(t)
	tripAfterFailure := staleTrips(1)[0]
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), tripAfterFailure.DriverID, tripAfterFailure.ID).Return(nil)
	m, _ := recordingMetrics(ctrl)
	reader := scriptedReader(ctrl, scanResult{err: errors.New("database is down")}, found(tripAfterFailure))
	h := start(t, context.Background(), config(1, 10), reader, push, m)

	h.ticker.tick(t)
	h.ticker.tick(t)
	if err := h.svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStopStopsScannerThenDrainsQueue(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := staleTrips(3)
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	first := push.EXPECT().RequestPosition(gomock.Any(), trips[0].DriverID, trips[0].ID).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
	for _, trip := range trips[1:] {
		push.EXPECT().RequestPosition(gomock.Any(), trip.DriverID, trip.ID).After(first).Return(nil)
	}
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(trips...)), push, m)

	h.ticker.tick(t)
	g.awaitStart(t, trips[0].ID)
	stopped := make(chan error, 1)
	go func() { stopped <- h.svc.Stop(context.Background()) }()
	<-h.ticker.stopped
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before the queue was drained: %v", err)
	default:
	}
	g.open()
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if c.requests(positions.ResultSuccess) != 3 {
		t.Fatalf("queued requests were lost: %v", c.finished)
	}
}

func TestStopDeadlineCancelsInFlightRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	trips := staleTrips(2)
	g := newGate()
	defer g.open()
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), trips[0].DriverID, trips[0].ID).DoAndReturn(
		func(ctx context.Context, _, id uuid.UUID) error { return g.wait(ctx, id) })
	m, c := recordingMetrics(ctrl)
	h := start(t, context.Background(), config(1, 10), scriptedReader(ctrl, found(trips...)), push, m)

	h.ticker.tick(t)
	g.awaitStart(t, trips[0].ID)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- h.svc.Stop(ctx) }()
	<-h.ticker.stopped
	cancel()
	err := <-stopped
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "1 queued") {
		t.Fatalf("Stop: got %v, want cancellation with one abandoned request", err)
	}
	if c.requests(positions.ResultError) != 1 {
		t.Fatalf("in-flight request must end with an error: %v", c.finished)
	}
}

func TestRealTickerTriggersScan(t *testing.T) {
	ctrl := gomock.NewController(t)
	polled := staleTrips(1)[0]
	done := make(chan struct{})
	push := NewMockpusher(ctrl)
	push.EXPECT().RequestPosition(gomock.Any(), polled.DriverID, polled.ID).DoAndReturn(
		func(context.Context, uuid.UUID, uuid.UUID) error {
			close(done)
			return nil
		})
	reader := NewMockstaleReader(ctrl)
	reader.EXPECT().ListStale(gomock.Any(), staleAfter, nil, batchSize).Return([]trip.StaleTrip{polled}, nil).MinTimes(1)
	m, _ := recordingMetrics(ctrl)
	cfg := config(1, 10)
	cfg.CheckInterval = time.Millisecond
	svc := positions.New(cfg, reader, push, m, slog.New(slog.DiscardHandler))
	svc.Start(context.Background())
	<-done
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStartAndStopAreIdempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	m, _ := recordingMetrics(ctrl)
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	svc := positions.New(config(2, 1), scriptedReader(ctrl), NewMockpusher(ctrl), m, logger,
		positions.WithTicker(func(time.Duration) positions.Ticker { return newManualTicker() }))
	if err := svc.Stop(context.Background()); err != nil {
		t.Fatalf("Stop before Start: %v", err)
	}
	svc.Start(context.Background())
	svc.Start(context.Background())
	for range 2 {
		if err := svc.Stop(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for _, msg := range []string{"stale trip polling started", "stale trip scanner stopped", "position workers stopped"} {
		if n := strings.Count(logs.String(), `"msg":"`+msg+`"`); n != 1 {
			t.Fatalf("%q logged %d times, want once:\n%s", msg, n, logs.String())
		}
	}
	if !strings.Contains(logs.String(), `"level":"INFO","msg":"stale trip polling started"`) ||
		!strings.Contains(logs.String(), `"workers":2`) || !strings.Contains(logs.String(), `"check_interval":"30s"`) {
		t.Fatalf("start log must be INFO with the pool settings:\n%s", logs.String())
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
