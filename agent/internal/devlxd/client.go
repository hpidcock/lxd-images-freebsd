// Package devlxd is a minimal client for the devlxd API served by the agent
// over its unix socket, used by the cloud-init glue.
package devlxd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// Client talks to the devlxd API over a unix socket.
type Client struct {
	http *http.Client
}

// New returns a client for the devlxd socket at the given path.
func New(socketPath string) *Client {
	return &Client{
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socketPath)
				},
				DisableKeepAlives: true,
			},
			Timeout: 30 * time.Second,
		},
	}
}

// NotFoundError is returned for 404 responses.
type NotFoundError struct {
	Path string
}

func (e *NotFoundError) Error() string {
	return e.Path + ": not found"
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://lxd"+path, nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, &NotFoundError{Path: path}
	default:
		return nil, fmt.Errorf("GET %s: %s: %s", path, resp.Status, string(body))
	}
}

// Wait waits until the devlxd API answers, or the context expires.
func (c *Client) Wait(ctx context.Context) error {
	for {
		_, err := c.get(ctx, "/1.0")
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("devlxd not reachable: %w", err)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// MetaData returns the cloud-init meta-data.
func (c *Client) MetaData(ctx context.Context) (string, error) {
	body, err := c.get(ctx, "/1.0/meta-data")
	return string(body), err
}

// ConfigKeys returns the config keys exposed by devlxd (e.g. cloud-init.user-data).
func (c *Client) ConfigKeys(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, "/1.0/config")
	if err != nil {
		return nil, err
	}

	var urls []string
	err = json.Unmarshal(body, &urls)
	if err != nil {
		return nil, fmt.Errorf("Failed parsing config key list: %w", err)
	}

	keys := make([]string, 0, len(urls))
	for _, u := range urls {
		const prefix = "/1.0/config/"
		if len(u) > len(prefix) && u[:len(prefix)] == prefix {
			keys = append(keys, u[len(prefix):])
		}
	}

	return keys, nil
}

// Config returns the value of a config key, or "" with a nil error if it is not set.
func (c *Client) Config(ctx context.Context, key string) (string, error) {
	body, err := c.get(ctx, "/1.0/config/"+key)
	if err != nil {
		var notFound *NotFoundError
		if ok := errorsAs(err, &notFound); ok {
			return "", nil
		}

		return "", err
	}

	return string(body), nil
}

func errorsAs(err error, target **NotFoundError) bool {
	e, ok := err.(*NotFoundError)
	if ok {
		*target = e
	}

	return ok
}
