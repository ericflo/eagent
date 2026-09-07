package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// sseEvent is one server-sent event.
type sseEvent struct {
	Event string
	Data  []byte
}

// post sends a JSON body and returns the response, mapping non-2xx to APIError.
func (c *Client) post(ctx context.Context, path string, body any, headers map[string]string) (*http.Response, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	dumpRequest(c.Endpoint.Model, raw)
	url := strings.TrimRight(c.Endpoint.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", c.UserAgent)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	for k, v := range c.Endpoint.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		ae := &APIError{Status: resp.StatusCode, Body: string(b)}
		var parsed struct {
			Error struct {
				Code    any    `json:"code"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &parsed) == nil {
			if s, ok := parsed.Error.Code.(string); ok {
				ae.Code = s
			}
			if ae.Code == "" {
				ae.Code = parsed.Error.Type
			}
		}
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(strings.TrimSpace(ra)); err == nil && secs > 0 {
				ae.RetryAfter = time.Duration(secs) * time.Second
			}
			ae.Body += " (retry-after " + ra + ")"
		}
		return nil, ae
	}
	return resp, nil
}

// readSSE consumes an event stream, calling fn for each event. It enforces the
// idle timeout by closing the body when no bytes arrive in time.
func (c *Client) readSSE(ctx context.Context, resp *http.Response, fn func(ev sseEvent) error) error {
	defer resp.Body.Close()
	idle := c.IdleTimeout
	if idle <= 0 {
		idle = 120 * time.Second
	}
	progress := make(chan struct{}, 1)
	done := make(chan struct{})
	var idleFired atomic.Bool
	go func() {
		t := time.NewTimer(idle)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-progress:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(idle)
			case <-t.C:
				idleFired.Store(true)
				resp.Body.Close()
				return
			case <-ctx.Done():
				resp.Body.Close()
				return
			}
		}
	}()
	defer close(done)

	r := bufio.NewReaderSize(resp.Body, 256<<10)
	var ev sseEvent
	var data bytes.Buffer
	flush := func() error {
		if data.Len() == 0 && ev.Event == "" {
			return nil
		}
		ev.Data = bytes.TrimSuffix(data.Bytes(), []byte("\n"))
		err := fn(ev)
		ev = sseEvent{}
		data.Reset()
		return err
	}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			select {
			case progress <- struct{}{}:
			default:
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if ferr := flush(); ferr != nil && !errors.Is(ferr, errStop) {
					return ferr
				}
				return nil
			}
			if idleFired.Load() {
				return ErrIdle
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("stream read: %w", err)
		}
		line = bytes.TrimRight(line, "\r\n")
		if len(line) == 0 {
			if err := flush(); err != nil {
				if errors.Is(err, errStop) {
					return nil
				}
				return err
			}
			continue
		}
		if line[0] == ':' {
			continue // comment / keepalive
		}
		field, value, _ := bytes.Cut(line, []byte(":"))
		value = bytes.TrimPrefix(value, []byte(" "))
		switch string(field) {
		case "event":
			ev.Event = string(value)
		case "data":
			data.Write(value)
			data.WriteByte('\n')
		}
	}
}

// errStop lets an SSE handler end the stream early without error.
var errStop = errors.New("stop")

// dumpRequest writes each request body to $EAGENT_DUMP_REQUESTS when that
// directory is set, for debugging prompt caching and provider quirks. Off
// by default; bodies contain the whole prompt.
func dumpRequest(model string, raw []byte) {
	dir := os.Getenv("EAGENT_DUMP_REQUESTS")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	name := fmt.Sprintf("%s-%s.json", time.Now().UTC().Format("150405.000000"), strings.NewReplacer("/", "_", ":", "_").Replace(model))
	_ = os.WriteFile(filepath.Join(dir, name), raw, 0o600)
}
