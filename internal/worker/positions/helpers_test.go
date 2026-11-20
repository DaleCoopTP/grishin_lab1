package positions_test

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
	"github.com/DaleCoopTP/grishin_lab1/internal/worker/positions"
)

const (
	checkInterval = 30 * time.Second
	staleAfter    = time.Minute
	batchSize     = 100
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// manualTicker delivers ticks only when the test asks. The channel is
// unbuffered, so tick returns once the scanner is back in its select loop,
// which means the previous pass has finished.
type manualTicker struct {
	ch      chan time.Time
	stopped chan struct{}
	once    sync.Once
}

func newManualTicker() *manualTicker {
	return &manualTicker{ch: make(chan time.Time), stopped: make(chan struct{})}
}

func (t *manualTicker) C() <-chan time.Time { return t.ch }

func (t *manualTicker) Stop() { t.once.Do(func() { close(t.stopped) }) }

func (t *manualTicker) tick(tb testing.TB) {
	tb.Helper()
	select {
	case t.ch <- time.Now():
	case <-t.stopped:
		tb.Fatal("tick after the scanner stopped")
	}
}

type scanResult struct {
	trips []trip.StaleTrip
	err   error
}

// scriptedReader returns the given results one per pass, then empty passes.
func scriptedReader(ctrl *gomock.Controller, results ...scanResult) *MockstaleReader {
	reader := NewMockstaleReader(ctrl)
	var mu sync.Mutex
	reader.EXPECT().ListStale(gomock.Any(), staleAfter, gomock.Any(), batchSize).DoAndReturn(
		func(context.Context, time.Duration, *trip.StaleTrip, int) ([]trip.StaleTrip, error) {
			mu.Lock()
			defer mu.Unlock()
			if len(results) == 0 {
				return nil, nil
			}
			next := results[0]
			results = results[1:]
			return next.trips, next.err
		}).AnyTimes()
	return reader
}

func found(trips ...trip.StaleTrip) scanResult {
	return scanResult{trips: trips}
}

func staleTrips(n int) []trip.StaleTrip {
	trips := make([]trip.StaleTrip, n)
	for i := range trips {
		trips[i] = trip.StaleTrip{ID: uuid.New(), DriverID: uuid.New(), LastSeenAt: time.Now().Add(-time.Duration(n-i) * time.Minute)}
	}
	return trips
}

type counters struct {
	mu       sync.Mutex
	stale    map[string]int
	scans    []int
	dropped  int
	finished map[string]int
}

func (c *counters) staleChecks(result string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stale[result]
}

func (c *counters) rowsPerScan() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.scans)
}

func (c *counters) requests(result string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	if result == positions.ResultDropped {
		return c.dropped
	}
	return c.finished[result]
}

func recordingMetrics(ctrl *gomock.Controller) (*Mockmetrics, *counters) {
	c := &counters{stale: map[string]int{}, finished: map[string]int{}}
	m := NewMockmetrics(ctrl)
	m.EXPECT().StaleCheck(gomock.Any(), gomock.Any()).Do(func(_ context.Context, result string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.stale[result]++
	}).AnyTimes()
	m.EXPECT().ScanFinished(gomock.Any(), gomock.Any()).Do(func(_ context.Context, rows int) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.scans = append(c.scans, rows)
	}).AnyTimes()
	m.EXPECT().RequestDropped(gomock.Any()).Do(func(context.Context) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.dropped++
	}).AnyTimes()
	m.EXPECT().RequestFinished(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(_ context.Context, result string, d time.Duration) {
		c.mu.Lock()
		defer c.mu.Unlock()
		if d < 0 {
			panic("negative duration")
		}
		c.finished[result]++
	}).AnyTimes()
	return m, c
}

// gate blocks push requests until released and reports when each one starts.
type gate struct {
	started chan uuid.UUID
	release chan struct{}
	once    sync.Once
}

func newGate() *gate {
	return &gate{started: make(chan uuid.UUID, 100), release: make(chan struct{})}
}

func (g *gate) wait(ctx context.Context, tripID uuid.UUID) error {
	g.started <- tripID
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (g *gate) open() { g.once.Do(func() { close(g.release) }) }

func (g *gate) awaitStart(tb testing.TB, want uuid.UUID) {
	tb.Helper()
	if got := <-g.started; got != want {
		tb.Fatalf("started push for %s, want %s", got, want)
	}
}

type harness struct {
	svc    *positions.Service
	ticker *manualTicker
}

func start(t *testing.T, ctx context.Context, cfg positions.Config, reader *MockstaleReader, push *Mockpusher, m *Mockmetrics) harness {
	t.Helper()
	ticker := newManualTicker()
	svc := positions.New(cfg, reader, push, m, slog.New(slog.DiscardHandler),
		positions.WithTicker(func(period time.Duration) positions.Ticker {
			if period != cfg.CheckInterval {
				t.Errorf("ticker period %s, want %s", period, cfg.CheckInterval)
			}
			return ticker
		}))
	svc.Start(ctx)
	t.Cleanup(func() {
		if err := svc.Stop(context.Background()); err != nil {
			t.Errorf("stop: %v", err)
		}
	})
	return harness{svc: svc, ticker: ticker}
}

func config(workers, queueSize int) positions.Config {
	return positions.Config{
		CheckInterval: checkInterval, StaleAfter: staleAfter,
		Workers: workers, QueueSize: queueSize, ScanBatchSize: batchSize, RequestTimeout: time.Minute,
	}
}
