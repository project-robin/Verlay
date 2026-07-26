package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

type Client struct {
	http *http.Client
}

func NewClient(socketPath string, timeout time.Duration) *Client {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "unix", socketPath)
		},
	}
	return &Client{http: &http.Client{Transport: transport, Timeout: timeout}}
}

func (c *Client) Call(ctx context.Context, method, path string, request any) (Envelope, error) {
	var body io.Reader
	if request != nil {
		b, err := json.Marshal(request)
		if err != nil {
			return Envelope{}, err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://mesh.local"+path, body)
	if err != nil {
		return Envelope{}, err
	}
	if request != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := c.http.Do(req)
	if err != nil {
		return Envelope{}, &APIError{Code: "daemon_unavailable", Message: "verlayd is unavailable", Retryable: true, Details: map[string]any{"cause": err.Error()}}
	}
	defer response.Body.Close()
	var envelope Envelope
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&envelope); err != nil {
		return Envelope{}, fmt.Errorf("decode daemon response: %w", err)
	}
	if !envelope.OK && envelope.Error == nil {
		return Envelope{}, fmt.Errorf("daemon returned an invalid error response")
	}
	return envelope, nil
}
