package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/DaleCoopTP/grishin_lab1/internal/config"
)

const (
	envCheckInterval = "POSITION_CHECK_INTERVAL"
	envStaleAfter    = "POSITION_STALE_AFTER"
	envWorkers       = "POSITION_WORKERS"
	envQueueSize     = "POSITION_QUEUE_SIZE"
	envPushURL       = "PUSH_HTTP_URL"
	envPushTimeout   = "PUSH_REQUEST_TIMEOUT"
)

func validEnv() map[string]string {
	return map[string]string{
		"HTTP_ADDR":                  ":8080",
		"HTTP_READ_TIMEOUT":          "15s",
		"HTTP_READ_HEADER_TIMEOUT":   "5s",
		"HTTP_WRITE_TIMEOUT":         "15s",
		"HTTP_IDLE_TIMEOUT":          "60s",
		"LOG_LEVEL":                  "info",
		"SHUTDOWN_TIMEOUT":           "10s",
		"DATABASE_URL":               "postgres://localhost:5432/tripgo?sslmode=disable",
		"DATABASE_MAX_CONNS":         "10",
		"DATABASE_MIN_CONNS":         "2",
		"DATABASE_MAX_CONN_LIFETIME": "30m",
		"DATABASE_CONNECT_TIMEOUT":   "5s",
		"DATABASE_QUERY_TIMEOUT":     "3s",
		envCheckInterval:             "30s",
		envStaleAfter:                "60s",
		envWorkers:                   "4",
		envQueueSize:                 "100",
		envPushURL:                   "http://localhost:8090/",
		envPushTimeout:               "1s",
	}
}

func setEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for name, value := range env {
		t.Setenv(name, value)
	}
}

func TestLoadReadsPositionSettings(t *testing.T) {
	setEnv(t, validEnv())

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PositionCheckInterval != 30*time.Second || cfg.PositionStaleAfter != time.Minute {
		t.Fatalf("intervals: got %s and %s", cfg.PositionCheckInterval, cfg.PositionStaleAfter)
	}
	if cfg.PositionWorkers != 4 || cfg.PositionQueueSize != 100 {
		t.Fatalf("pool: got %d workers and queue %d", cfg.PositionWorkers, cfg.PositionQueueSize)
	}
	if cfg.PushHTTPURL != "http://localhost:8090" || cfg.PushRequestTimeout != time.Second {
		t.Fatalf("push: got %q and %s", cfg.PushHTTPURL, cfg.PushRequestTimeout)
	}
}

func TestLoadRejectsInvalidPositionSettings(t *testing.T) {
	tests := []struct {
		name  string
		env   string
		value string
		want  string
	}{
		{name: "missing interval", env: envCheckInterval, value: "", want: "POSITION_CHECK_INTERVAL is required"},
		{name: "zero interval", env: envCheckInterval, value: "0s", want: "POSITION_CHECK_INTERVAL must be positive"},
		{name: "malformed stale period", env: envStaleAfter, value: "minute", want: "POSITION_STALE_AFTER"},
		{name: "missing workers", env: envWorkers, value: "", want: "POSITION_WORKERS is required"},
		{name: "zero workers", env: envWorkers, value: "0", want: "POSITION_WORKERS must be positive"},
		{name: "fractional workers", env: envWorkers, value: "1.5", want: "POSITION_WORKERS"},
		{name: "negative queue", env: envQueueSize, value: "-1", want: "POSITION_QUEUE_SIZE must be positive"},
		{name: "missing push url", env: envPushURL, value: "", want: "PUSH_HTTP_URL is required"},
		{name: "push url without scheme", env: envPushURL, value: "localhost:8090", want: "PUSH_HTTP_URL must be an absolute http or https URL"},
		{name: "push url without host", env: envPushURL, value: "http://", want: "PUSH_HTTP_URL must be an absolute http or https URL"},
		{name: "negative push timeout", env: envPushTimeout, value: "-1s", want: "PUSH_REQUEST_TIMEOUT must be positive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := validEnv()
			env[tt.env] = tt.value
			setEnv(t, env)

			_, err := config.Load()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got error %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
