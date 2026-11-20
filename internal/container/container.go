package container

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"

	"github.com/DaleCoopTP/grishin_lab1/internal/config"
	"github.com/DaleCoopTP/grishin_lab1/internal/gateway/push"
	httptransport "github.com/DaleCoopTP/grishin_lab1/internal/http"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/health"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/middleware"
	httppositioncreate "github.com/DaleCoopTP/grishin_lab1/internal/http/position/create"
	httppositionlist "github.com/DaleCoopTP/grishin_lab1/internal/http/position/list"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/ready"
	"github.com/DaleCoopTP/grishin_lab1/internal/http/response"
	httpcreate "github.com/DaleCoopTP/grishin_lab1/internal/http/trip/create"
	httpfinish "github.com/DaleCoopTP/grishin_lab1/internal/http/trip/finish"
	httpget "github.com/DaleCoopTP/grishin_lab1/internal/http/trip/get"
	"github.com/DaleCoopTP/grishin_lab1/internal/infra/postgres"
	"github.com/DaleCoopTP/grishin_lab1/internal/infra/postgres/transaction"
	"github.com/DaleCoopTP/grishin_lab1/internal/infra/telemetry"
	triprepository "github.com/DaleCoopTP/grishin_lab1/internal/repository/trip"
	positionlist "github.com/DaleCoopTP/grishin_lab1/internal/usecase/position/list"
	positionsave "github.com/DaleCoopTP/grishin_lab1/internal/usecase/position/save"
	"github.com/DaleCoopTP/grishin_lab1/internal/usecase/trip/create"
	"github.com/DaleCoopTP/grishin_lab1/internal/usecase/trip/finish"
	"github.com/DaleCoopTP/grishin_lab1/internal/usecase/trip/get"
	"github.com/DaleCoopTP/grishin_lab1/internal/worker/positions"
)

type Container struct {
	Server         *http.Server
	Logger         *slog.Logger
	positions      *positions.Service
	closeResources func(context.Context)
	closeOnce      sync.Once
	closed         chan struct{}
}

func New(ctx context.Context, cfg config.Config) (_ *Container, err error) {
	logger := telemetry.NewLogger(cfg.LogLevel)
	sdk := telemetry.NewSDK(ctx, logger)
	var closePool, closePush func()
	closeResources := func(ctx context.Context) {
		if closePush != nil {
			closePush()
		}
		if err := sdk.Shutdown(ctx); err != nil {
			logger.WarnContext(ctx, "telemetry shutdown failed", "error.type", fmt.Sprintf("%T", err))
		}
		if closePool != nil {
			closePool()
		}
	}
	defer func() {
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
			defer cancel()
			closeResources(closeCtx)
		}
	}()
	meter := sdk.Metrics.Meter("trip-service")
	queryTracer, err := postgres.NewQueryTracer(sdk.Traces, meter)
	if err != nil {
		return nil, err
	}
	pool, err := postgres.Open(ctx, cfg, queryTracer)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	closePool = pool.Close
	txManager := transaction.NewTxManager(pool, cfg.DatabaseQueryTimeout)
	repository := triprepository.NewRepository(txManager, cfg.DatabaseQueryTimeout)
	events, err := telemetry.NewTripEvents(meter, logger, repository)
	if err != nil {
		return nil, err
	}
	positionEvents, err := telemetry.NewPositionEvents(meter, logger)
	if err != nil {
		return nil, err
	}
	pushClient := push.New(cfg.PushHTTPURL, cfg.PushRequestTimeout, push.WithTransport(func(next http.RoundTripper) http.RoundTripper {
		return otelhttp.NewTransport(next,
			otelhttp.WithTracerProvider(sdk.Traces),
			otelhttp.WithMeterProvider(metricnoop.NewMeterProvider()),
			otelhttp.WithPropagators(propagation.TraceContext{}),
			otelhttp.WithSpanNameFormatter(func(_ string, r *http.Request) string { return "push " + r.Method + " " + r.URL.Path }),
		)
	}))
	closePush = pushClient.Close
	positionRequests, err := telemetry.NewPositionRequests(meter)
	if err != nil {
		return nil, err
	}
	poller := positions.New(positions.Config{
		CheckInterval:  cfg.PositionCheckInterval,
		StaleAfter:     cfg.PositionStaleAfter,
		Workers:        cfg.PositionWorkers,
		QueueSize:      cfg.PositionQueueSize,
		ScanBatchSize:  cfg.PositionScanBatchSize,
		RequestTimeout: cfg.PushRequestTimeout,
	}, repository, pushClient, positionRequests, logger, positions.WithTracer(sdk.Traces.Tracer("trip-service/positions")))
	if err := positionRequests.ObservePool(poller); err != nil {
		return nil, err
	}
	createTrip := create.New(repository, txManager, events)
	getTrip := get.New(repository)
	finishTrip := finish.New(repository, txManager, events)
	savePosition := positionsave.New(repository, txManager, poller, positionEvents)
	listPositions := positionlist.New(repository)
	writer := response.New()
	createHandler := httpcreate.New(createTrip, writer)
	getHandler := httpget.New(getTrip, writer)
	finishHandler := httpfinish.New(finishTrip, writer)
	createPositionHandler := httppositioncreate.New(savePosition, writer)
	listPositionsHandler := httppositionlist.New(listPositions, writer)
	healthHandler := health.New(writer)
	readyHandler := ready.New(pool, cfg.DatabaseQueryTimeout, writer)
	router := httptransport.NewRouter(createHandler, getHandler, finishHandler,
		createPositionHandler, listPositionsHandler, healthHandler, readyHandler, writer)
	handler, err := middleware.Wrap(router, writer, logger, sdk.Traces, meter)
	if err != nil {
		return nil, err
	}
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           handler,
		ReadTimeout:       cfg.HTTPReadTimeout,
		ReadHeaderTimeout: cfg.HTTPReadHeaderTimeout,
		WriteTimeout:      cfg.HTTPWriteTimeout,
		IdleTimeout:       cfg.HTTPIdleTimeout,
	}

	return &Container{
		Server:         server,
		Logger:         logger,
		positions:      poller,
		closeResources: closeResources,
		closed:         make(chan struct{}),
	}, nil
}

func (c *Container) StartBackground(ctx context.Context) {
	if c.positions != nil {
		c.positions.Start(context.WithoutCancel(ctx))
	}
}

func (c *Container) StopBackground(ctx context.Context) error {
	if c.positions == nil {
		return nil
	}
	return c.positions.Stop(ctx)
}

func (c *Container) Close(ctx context.Context) error {
	c.closeOnce.Do(func() {
		go func() {
			c.closeResources(ctx)
			close(c.closed)
		}()
	})
	select {
	case <-c.closed:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close application resources: %w", ctx.Err())
	}
}
