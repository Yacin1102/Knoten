package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"knoten/internal/protocol"
)

const maxResponseBytes = 8 << 20

type Client struct {
	baseURL string
	http    *http.Client
	token   string
}

func New(baseURL, token string) (*Client, error) {

	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")

	if baseURL == "" {
		return nil, errors.New("no coordination server URL configured")
	}

	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("%q is not a valid URL: %w", baseURL, err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("server URL %q must start with http:// or https://", baseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("server URL %q has no host part", baseURL)
	}

	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
		token:   token,
	}, nil
}

func (c *Client) Register(ctx context.Context, req protocol.RegisterRequest) (protocol.RegisterResponse, error) {

	req.JoinToken = c.token

	var resp protocol.RegisterResponse
	if err := c.post(ctx, protocol.PathRegister, req, &resp); err != nil {
		return protocol.RegisterResponse{}, err
	}

	if resp.VPNIP == "" {
		return protocol.RegisterResponse{}, errors.New("the server did not return a VPN address")
	}
	if resp.VPNCIDR == "" {
		return protocol.RegisterResponse{}, errors.New("the server did not return a VPN range")
	}

	return resp, nil
}

func (c *Client) Sync(ctx context.Context, req protocol.SyncRequest) (protocol.SyncResponse, error) {

	req.JoinToken = c.token

	var resp protocol.SyncResponse
	if err := c.post(ctx, protocol.PathSync, req, &resp); err != nil {
		return protocol.SyncResponse{}, err
	}

	if resp.SyncIntervalSeconds <= 0 {
		resp.SyncIntervalSeconds = protocol.DefaultSyncIntervalSeconds
	}

	return resp, nil
}

func (c *Client) post(ctx context.Context, path string, body any, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("could not encode the request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("could not build the request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the coordination server: %w", err)
	}

	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("could not read the server's reply: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return c.translateError(resp.StatusCode, data)
	}

	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("the server sent a reply we could not understand (is %s really a coordination server?): %w", c.baseURL, err)
	}

	return nil
}

func (c *Client) translateError(status int, body []byte) error {
	var errResp protocol.ErrorResponse
	message := ""
	if err := json.Unmarshal(body, &errResp); err == nil && errResp.Error != "" {
		message = errResp.Error
	} else {
		message = snippet(body)
	}

	switch status {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s", protocol.ErrUnknownMachine, message)

	case http.StatusUnauthorized:
		return fmt.Errorf("the coordination server rejected our join token: %s", message)

	default:
		return fmt.Errorf("coordination server returned HTTP %d: %s", status, message)
	}
}

func snippet(b []byte) string {
	const limit = 200
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "(empty response body)"
	}
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}