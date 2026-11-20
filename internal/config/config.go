package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTPAddr                string
	HTTPReadTimeout         time.Duration
	HTTPReadHeaderTimeout   time.Duration
	HTTPWriteTimeout        time.Duration
	HTTPIdleTimeout         time.Duration
	LogLevel                slog.Level
	ShutdownTimeout         time.Duration
	DatabaseURL             string
	DatabaseMaxConns        int32
	DatabaseMinConns        int32
	DatabaseMaxConnLifetime time.Duration
	DatabaseConnectTimeout  time.Duration
	DatabaseQueryTimeout    time.Duration
	PositionCheckInterval   time.Duration
	PositionStaleAfter      time.Duration
	PositionWorkers         int
	PositionQueueSize       int
	PositionScanBatchSize   int
	PushHTTPURL             string
	PushRequestTimeout      time.Duration
}

func Load() (Config, error) {
	r := &envReader{}
	cfg := Config{
		HTTPAddr:                r.required("HTTP_ADDR"),
		HTTPReadTimeout:         r.duration("HTTP_READ_TIMEOUT"),
		HTTPReadHeaderTimeout:   r.duration("HTTP_READ_HEADER_TIMEOUT"),
		HTTPWriteTimeout:        r.duration("HTTP_WRITE_TIMEOUT"),
		HTTPIdleTimeout:         r.duration("HTTP_IDLE_TIMEOUT"),
		LogLevel:                r.logLevel("LOG_LEVEL"),
		ShutdownTimeout:         r.duration("SHUTDOWN_TIMEOUT"),
		DatabaseURL:             r.required("DATABASE_URL"),
		DatabaseMaxConns:        r.int32Value("DATABASE_MAX_CONNS", false),
		DatabaseMinConns:        r.int32Value("DATABASE_MIN_CONNS", true),
		DatabaseMaxConnLifetime: r.duration("DATABASE_MAX_CONN_LIFETIME"),
		DatabaseConnectTimeout:  r.duration("DATABASE_CONNECT_TIMEOUT"),
		DatabaseQueryTimeout:    r.duration("DATABASE_QUERY_TIMEOUT"),
		PositionCheckInterval:   r.duration("POSITION_CHECK_INTERVAL"),
		PositionStaleAfter:      r.duration("POSITION_STALE_AFTER"),
		PositionWorkers:         r.positiveInt("POSITION_WORKERS"),
		PositionQueueSize:       r.positiveInt("POSITION_QUEUE_SIZE"),
		PositionScanBatchSize:   r.positiveInt("POSITION_SCAN_BATCH_SIZE"),
		PushHTTPURL:             r.httpURL("PUSH_HTTP_URL"),
		PushRequestTimeout:      r.duration("PUSH_REQUEST_TIMEOUT"),
	}
	if r.err != nil {
		return Config{}, r.err
	}
	if cfg.DatabaseMinConns > cfg.DatabaseMaxConns {
		return Config{}, fmt.Errorf("DATABASE_MIN_CONNS must not exceed DATABASE_MAX_CONNS")
	}
	return cfg, nil
}

type envReader struct {
	err error
}

func (r *envReader) required(name string) string {
	if r.err != nil {
		return ""
	}
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		r.err = fmt.Errorf("%s is required", name)
	}
	return value
}

func (r *envReader) duration(name string) time.Duration {
	value := r.required(name)
	if r.err != nil {
		return 0
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		r.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	if parsed <= 0 {
		r.err = fmt.Errorf("%s must be positive", name)
	}
	return parsed
}

func (r *envReader) logLevel(name string) slog.Level {
	value := r.required(name)
	if r.err != nil {
		return 0
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(value)); err != nil {
		r.err = fmt.Errorf("%s: %w", name, err)
	}
	return level
}

func (r *envReader) int32Value(name string, allowZero bool) int32 {
	value := r.required(name)
	if r.err != nil {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil {
		r.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	if parsed < 0 || (!allowZero && parsed == 0) {
		if allowZero {
			r.err = fmt.Errorf("%s must be non-negative", name)
		} else {
			r.err = fmt.Errorf("%s must be positive", name)
		}
	}
	return int32(parsed)
}

func (r *envReader) positiveInt(name string) int {
	value := r.required(name)
	if r.err != nil {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		r.err = fmt.Errorf("%s: %w", name, err)
		return 0
	}
	if parsed <= 0 {
		r.err = fmt.Errorf("%s must be positive", name)
	}
	return parsed
}

func (r *envReader) httpURL(name string) string {
	value := r.required(name)
	if r.err != nil {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		r.err = fmt.Errorf("%s: %w", name, err)
		return ""
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		r.err = fmt.Errorf("%s must be an absolute http or https URL", name)
	}
	return strings.TrimRight(value, "/")
}
