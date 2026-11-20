package push

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const (
	pushPath            = "/api/v1/push"
	kindRequestPosition = "REQUEST_POSITION"
	maxIdleConnsPerHost = 32
	maxDrainBytes       = 64 << 10
)

type StatusError struct {
	StatusCode int
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("push service responded with status %d", e.StatusCode)
}

type Option func(*Client)

func WithTransport(wrap func(http.RoundTripper) http.RoundTripper) Option {
	return func(c *Client) {
		c.client.Transport = wrap(c.client.Transport)
	}
}

type Client struct {
	client    *http.Client
	transport *http.Transport
	endpoint  string
}

func New(baseURL string, timeout time.Duration, opts ...Option) *Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		IdleConnTimeout:       90 * time.Second,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
	}
	c := &Client{
		client:    &http.Client{Transport: transport, Timeout: timeout},
		transport: transport,
		endpoint:  baseURL + pushPath,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type pushRequest struct {
	RequestID   string            `json:"request_id"`
	RecipientID uuid.UUID         `json:"recipient_id"`
	Kind        string            `json:"kind"`
	Data        map[string]string `json:"data"`
}

func (c *Client) RequestPosition(ctx context.Context, driverID, tripID uuid.UUID) error {
	body, err := json.Marshal(pushRequest{
		RequestID:   uuid.NewString(),
		RecipientID: driverID,
		Kind:        kindRequestPosition,
		Data:        map[string]string{"trip_id": tripID.String()},
	})
	if err != nil {
		return fmt.Errorf("encode push request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("send push request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxDrainBytes))
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != http.StatusAccepted {
		return &StatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

func (c *Client) Close() {
	c.transport.CloseIdleConnections()
}
