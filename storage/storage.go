package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client talks to a PRIVATE Supabase Storage bucket using the service-role key (server-side only).
type Client struct {
	base, bucket, key string
	hc                *http.Client
}

func New(supabaseURL, bucket, serviceKey string) *Client {
	return &Client{base: strings.TrimRight(supabaseURL, "/"), bucket: bucket, key: serviceKey, hc: &http.Client{Timeout: 60 * time.Second}}
}

func (c *Client) req(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	r, err := http.NewRequestWithContext(ctx, method, c.base+"/storage/v1/object/"+c.bucket+"/"+strings.TrimLeft(path, "/"), body)
	if err != nil {
		return nil, err
	}
	r.Header.Set("Authorization", "Bearer "+c.key)
	r.Header.Set("apikey", c.key)
	return r, nil
}

func (c *Client) Upload(ctx context.Context, path, contentType string, data []byte) error {
	r, err := c.req(ctx, http.MethodPost, path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("x-upsert", "false")
	resp, err := c.hc.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("storage upload: http %d %s", resp.StatusCode, b)
	}
	return nil
}

// Open returns the streaming response for an object (caller closes Body). Range is passed through.
func (c *Client) Open(ctx context.Context, path, rng string) (*http.Response, error) {
	r, err := c.req(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if rng != "" {
		r.Header.Set("Range", rng)
	}
	resp, err := c.hc.Do(r)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, fmt.Errorf("storage open: http %d", resp.StatusCode)
	}
	return resp, nil
}
