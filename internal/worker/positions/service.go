package positions

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

const (
	scanSpanName    = "positions.scan"
	requestSpanName = "positions.request"
	tripIDKey       = "tripgo.trip.id"
)

type Config struct {
	CheckInterval  time.Duration
	StaleAfter     time.Duration
	Workers        int
	QueueSize      int
	RequestTimeout time.Duration
}

type Option func(*Service)

// WithTracer sets the tracer for scan passes and push requests.
func WithTracer(tracer trace.Tracer) Option {
	return func(s *Service) { s.tracer = tracer }
}

// WithTicker replaces the real ticker, used by tests.
func WithTicker(newTicker func(period time.Duration) Ticker) Option {
	return func(s *Service) { s.newTicker = newTicker }
}

// Service polls trips without fresh positions: one scanner goroutine puts them
// into a bounded queue and a fixed pool of workers asks Push Service for a
// position.
type Service struct {
	cfg       Config
	reader    staleReader
	pusher    pusher
	metrics   metrics
	logger    *slog.Logger
	newTicker func(time.Duration) Ticker
	tracer    trace.Tracer
	queue     *queue
	active    atomic.Int64

	started     atomic.Bool
	stopScanner context.CancelFunc
	stopWorkers context.CancelFunc
	scannerDone chan struct{}
	workersDone chan struct{}
	stopOnce    sync.Once
}

func New(cfg Config, reader staleReader, pusher pusher, metrics metrics, logger *slog.Logger, opts ...Option) *Service {
	s := &Service{
		cfg: cfg, reader: reader, pusher: pusher, metrics: metrics, logger: logger,
		newTicker:   newRealTicker,
		tracer:      noop.NewTracerProvider().Tracer(""),
		queue:       newQueue(cfg.QueueSize),
		scannerDone: make(chan struct{}),
		workersDone: make(chan struct{}),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Start launches the scanner and the workers. Cancelling ctx stops them at
// once, dropping queued tasks; Stop is the graceful way.
func (s *Service) Start(ctx context.Context) {
	if !s.started.CompareAndSwap(false, true) {
		return
	}
	scanCtx, stopScanner := context.WithCancel(ctx)
	workCtx, stopWorkers := context.WithCancel(ctx)
	s.stopScanner, s.stopWorkers = stopScanner, stopWorkers

	var workers sync.WaitGroup
	for range s.cfg.Workers {
		workers.Go(func() { s.work(workCtx) })
	}
	go func() {
		workers.Wait()
		close(s.workersDone)
	}()
	go s.scan(scanCtx)
}

// Stop stops the scanner first so no new tasks appear, then lets the workers
// finish the queue. If ctx expires first, in-flight requests are cancelled and
// the rest of the queue is abandoned.
func (s *Service) Stop(ctx context.Context) error {
	if !s.started.Load() {
		return nil
	}
	s.stopOnce.Do(func() {
		s.stopScanner()
		<-s.scannerDone
		s.queue.close()
	})
	select {
	case <-s.workersDone:
		s.stopWorkers()
		return nil
	case <-ctx.Done():
		s.stopWorkers()
		<-s.workersDone
		abandoned := s.queue.len()
		s.logger.WarnContext(ctx, "position workers stopped before the queue was drained", "abandoned", abandoned)
		return fmt.Errorf("stop position workers with %d queued requests: %w", abandoned, ctx.Err())
	}
}

// Cancel removes a trip from the queue because its position has arrived.
func (s *Service) Cancel(tripID uuid.UUID) {
	s.queue.cancel(tripID)
}

func (s *Service) QueueSize() int {
	return s.queue.len()
}

func (s *Service) ActiveWorkers() int {
	return int(s.active.Load())
}

// scan runs passes one after another in a single goroutine, so ticks never
// overlap: a tick that arrives during a pass is dropped by time.Ticker.
func (s *Service) scan(ctx context.Context) {
	defer close(s.scannerDone)
	ticker := s.newTicker(s.cfg.CheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C():
			s.scanOnce(ctx)
		}
	}
}

func (s *Service) scanOnce(ctx context.Context) {
	ctx, span := s.tracer.Start(ctx, scanSpanName)
	defer span.End()
	trips, err := s.reader.ListStale(ctx, s.cfg.StaleAfter)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "stale trips scan failed")
		if ctx.Err() == nil {
			s.logger.ErrorContext(ctx, "stale trips scan failed", "error", err.Error())
		}
		return
	}
	counts := map[string]int{}
	for _, trip := range trips {
		result := s.queue.offer(task{tripID: trip.ID, driverID: trip.DriverID, scan: span.SpanContext()})
		counts[result]++
		s.metrics.StaleCheck(ctx, result)
		if result == ResultDropped {
			s.metrics.RequestDropped(ctx)
		}
	}
	span.SetAttributes(
		attribute.Int("tripgo.stale_trips", len(trips)),
		attribute.Int("tripgo.stale_checks.queued", counts[ResultQueued]),
		attribute.Int("tripgo.stale_checks.duplicate", counts[ResultDuplicate]),
		attribute.Int("tripgo.stale_checks.dropped", counts[ResultDropped]),
	)
	if dropped := counts[ResultDropped]; dropped > 0 {
		s.logger.WarnContext(ctx, "position request queue is full, trips skipped until the next tick",
			"dropped", dropped, "queue_size", s.cfg.QueueSize)
	}
}

func (s *Service) work(ctx context.Context) {
	for ctx.Err() == nil {
		t, ok := s.queue.take(ctx)
		if !ok || ctx.Err() != nil {
			return
		}
		s.process(ctx, t)
	}
}

// process runs in its own trace: the request outlives the scan pass that
// queued it, so the scan span is linked rather than used as the parent.
func (s *Service) process(ctx context.Context, t task) {
	if !s.queue.claim(t) {
		s.logger.DebugContext(ctx, "position request cancelled by a new position", "trip_id", t.tripID)
		return
	}
	defer s.queue.done(t)
	s.active.Add(1)
	defer s.active.Add(-1)

	ctx, span := s.tracer.Start(ctx, requestSpanName,
		trace.WithNewRoot(),
		trace.WithLinks(trace.Link{SpanContext: t.scan}),
		trace.WithAttributes(attribute.String(tripIDKey, t.tripID.String())),
	)
	defer span.End()
	requestCtx, cancel := context.WithTimeout(ctx, s.cfg.RequestTimeout)
	defer cancel()
	started := time.Now()
	err := s.request(requestCtx, t)
	result := ResultSuccess
	if err != nil {
		result = ResultError
		span.RecordError(err)
		span.SetStatus(codes.Error, "position request failed")
		s.logger.WarnContext(ctx, "position request failed", "trip_id", t.tripID, "error", err.Error())
	}
	s.metrics.RequestFinished(ctx, result, time.Since(started))
}

// request keeps a panicking pusher from killing the worker.
func (s *Service) request(ctx context.Context, t task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(errors.New("push request panicked"), fmt.Errorf("%v", r))
		}
	}()
	return s.pusher.RequestPosition(ctx, t.driverID, t.tripID)
}
