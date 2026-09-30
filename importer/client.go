package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Track struct{ ProviderID, Title, Artist, Album, Genre, ArtworkURL, PreviewURL string }

// Provider abstracts the external catalog so another (legally usable) source can be plugged in.
type Provider interface {
	Name() string
	// Tracks pages through source ("playlist:<id>" or "search:<query>"), calling yield per track.
	Tracks(ctx context.Context, source string, limit int, yield func(Track) error) error
	// Fetch downloads an audio URL. Only called when audio download is explicitly enabled.
	Fetch(ctx context.Context, url string) (data []byte, contentType string, err error)
}

func NewProvider(name, baseURL, key string) (Provider, error) {
	switch strings.ToLower(name) {
	case "deezer":
		return &deezer{base: strings.TrimRight(baseURL, "/"), key: key, hc: &http.Client{Timeout: 30 * time.Second}}, nil
	}
	return nil, fmt.Errorf("unsupported provider %q: implement importer.Provider for it", name)
}

type deezer struct {
	base, key string
	hc        *http.Client
}

func (d *deezer) Name() string { return "deezer" }

func isQuota(b []byte) bool {
	var e struct {
		Error *struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(b, &e) == nil && e.Error != nil && e.Error.Code == 4
}

// get retries on network errors, 429/5xx (honoring Retry-After) and the provider's in-body quota error.
func (d *deezer) get(ctx context.Context, u string, withKey bool) ([]byte, string, error) {
	var last error
	for attempt := 0; attempt < 5; attempt++ {
		wait := time.Duration(1<<attempt) * time.Second
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, "", err
		}
		if withKey && d.key != "" {
			req.Header.Set("Authorization", "Bearer "+d.key)
		}
		resp, err := d.hc.Do(req)
		if err != nil {
			last = err
		} else {
			body, rerr := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
			resp.Body.Close()
			switch {
			case rerr != nil:
				last = rerr
			case resp.StatusCode == 429 || resp.StatusCode >= 500:
				last = fmt.Errorf("http %d", resp.StatusCode)
				if s, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil {
					wait = time.Duration(s) * time.Second
				}
			case resp.StatusCode >= 400:
				return nil, "", fmt.Errorf("http %d", resp.StatusCode)
			case isQuota(body):
				last, wait = fmt.Errorf("provider quota exceeded"), 5*time.Second
			default:
				return body, resp.Header.Get("Content-Type"), nil
			}
		}
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(wait):
		}
	}
	return nil, "", fmt.Errorf("giving up: %w", last)
}

func (d *deezer) Fetch(ctx context.Context, u string) ([]byte, string, error) { return d.get(ctx, u, false) }

func (d *deezer) Tracks(ctx context.Context, source string, limit int, yield func(Track) error) error {
	kind, arg, ok := strings.Cut(source, ":")
	if !ok || arg == "" {
		return fmt.Errorf(`source must look like "playlist:<id>" or "search:<query>"`)
	}
	path := "/search"
	switch kind {
	case "playlist":
		path = "/playlist/" + url.PathEscape(arg) + "/tracks"
	case "search":
	default:
		return fmt.Errorf("unknown source kind %q", kind)
	}
	n := 0
	for offset := 0; ; {
		q := url.Values{"index": {strconv.Itoa(offset)}, "limit": {"100"}}
		if kind == "search" {
			q.Set("q", arg)
		}
		body, _, err := d.get(ctx, d.base+path+"?"+q.Encode(), true)
		if err != nil {
			return err
		}
		var page struct {
			Data []struct {
				ID      int64  `json:"id"`
				Title   string `json:"title"`
				Preview string `json:"preview"`
				Artist  struct {
					Name string `json:"name"`
				} `json:"artist"`
				Album struct {
					Title string `json:"title"`
					Cover string `json:"cover_xl"`
				} `json:"album"`
			} `json:"data"`
			Next string `json:"next"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return err
		}
		for _, t := range page.Data {
			if limit > 0 && n >= limit {
				return nil
			}
			n++
			if err := yield(Track{ProviderID: strconv.FormatInt(t.ID, 10), Title: t.Title, Artist: t.Artist.Name, Album: t.Album.Title, ArtworkURL: t.Album.Cover, PreviewURL: t.Preview}); err != nil {
				return err
			}
		}
		if page.Next == "" || len(page.Data) == 0 {
			return nil
		}
		offset += len(page.Data)
	}
}
