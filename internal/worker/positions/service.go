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

	"github.com/DaleCoopTP/grishin_lab1/internal/domain/trip"
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
	ScanBatchSize  int
	RequestTimeout time.Duration
}

type Option func(*Service)

func WithTracer(tracer trace.Tracer) Option {
	return func(s *Service) { s.tracer = tracer }
}

func WithTicker(newTicker func(period time.Duration) Ticker) Option {
	return func(s *Service) { s.newTicker = newTicker }
}

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
		s.logger.InfoContext(workCtx, "position workers stopped")
		close(s.workersDone)
	}()
	go s.scan(scanCtx)
	s.logger.InfoContext(ctx, "stale trip polling started",
		"check_interval", s.cfg.CheckInterval.String(),
		"stale_after", s.cfg.StaleAfter.String(),
		"workers", s.cfg.Workers,
		"queue_size", s.cfg.QueueSize,
		"scan_batch_size", s.cfg.ScanBatchSize,
	)
}

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

func (s *Service) Cancel(tripID uuid.UUID) {
	s.queue.cancel(tripID)
}

func (s *Service) QueueSize() int {
	return s.queue.len()
}

func (s *Service) ActiveWorkers() int {
	return int(s.active.Load())
}

func (s *Service) scan(ctx context.Context) {
	defer close(s.scannerDone)
	defer s.logger.InfoContext(ctx, "stale trip scanner stopped")
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
	counts := map[string]int{}
	rows, batches, evicted := 0, 0, 0
	defer func() {
		s.metrics.ScanFinished(ctx, rows)
		span.SetAttributes(
			attribute.Int("tripgo.stale_trips.rows", rows),
			attribute.Int("tripgo.stale_trips.batches", batches),
			attribute.Int("tripgo.stale_checks.queued", counts[ResultQueued]),
			attribute.Int("tripgo.stale_checks.duplicate", counts[ResultDuplicate]),
			attribute.Int("tripgo.stale_checks.dropped", counts[ResultDropped]),
			attribute.Int("tripgo.stale_checks.evicted", evicted),
		)
		if counts[ResultDropped]+evicted > 0 {
			s.logger.WarnContext(ctx, "position request queue is full, trips skipped until the next tick",
				"dropped", counts[ResultDropped], "evicted", evicted, "queue_size", s.cfg.QueueSize)
		}
	}()

	var after *trip.StaleTrip
	for {
		batch, err := s.reader.ListStale(ctx, s.cfg.StaleAfter, after, s.cfg.ScanBatchSize)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "stale trips scan failed")
			if ctx.Err() == nil {
				s.logger.ErrorContext(ctx, "stale trips scan failed", "error", err.Error(), "batch", batches+1)
			}
			return
		}
		batches++
		rows += len(batch)
		overflow := false
		for _, stale := range batch {
			offered := s.queue.offer(task{
				tripID: stale.ID, driverID: stale.DriverID, lastSeenAt: stale.LastSeenAt, scan: span.SpanContext(),
			})
			counts[offered.result]++
			s.metrics.StaleCheck(ctx, offered.result)
			if offered.result == ResultDropped || offered.evicted {
				overflow = true
				s.metrics.RequestDropped(ctx)
			}
			if offered.evicted {
				evicted++
			}
		}
		if overflow || len(batch) < s.cfg.ScanBatchSize {
			return
		}
		after = &batch[len(batch)-1]
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

func (s *Service) request(ctx context.Context, t task) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.Join(errors.New("push request panicked"), fmt.Errorf("%v", r))
		}
	}()
	return s.pusher.RequestPosition(ctx, t.driverID, t.tripID)
}
