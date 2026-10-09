package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	sdk "github.com/azrtydxb/go-ai-sdk/mcp"
)

var errSSEClosed = errors.New("mcp sse transport closed")

// sseTransport implements the legacy HTTP+SSE transport (protocol 2024-11-05):
// a long-lived GET stream delivers an "endpoint" event with the URL to POST
// requests to, then "message" events carrying JSON-RPC messages.
type sseTransport struct {
	client  *http.Client
	headers map[string]string
	base    *url.URL

	cancel   context.CancelFunc
	recv     chan json.RawMessage
	done     chan struct{}
	endpoint chan string
	once     sync.Once

	mu     sync.Mutex
	post   string
	failed error
}

// newSSETransport opens the event stream and waits for the endpoint event.
func newSSETransport(ctx context.Context, rawURL string, headers map[string]string, client *http.Client) (sdk.Transport, error) {
	base, err := url.Parse(rawURL)
	if err != nil {
		return nil, err
	}
	streamCtx, cancel := context.WithCancel(context.Background())
	t := &sseTransport{client: client, headers: headers, base: base, cancel: cancel, recv: make(chan json.RawMessage, 64), done: make(chan struct{}), endpoint: make(chan string, 1)}
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	// The response arrives only once the server starts streaming, so connect
	// in the background and bound the wait by ctx.
	started := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if err != nil {
			started <- err
			t.fail(err)
			return
		}
		if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
			resp.Body.Close()
			err := fmt.Errorf("sse: unexpected response %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
			started <- err
			t.fail(err)
			return
		}
		started <- nil
		t.read(resp.Body)
	}()
	select {
	case err := <-started:
		if err != nil {
			cancel()
			return nil, err
		}
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	select {
	case ep := <-t.endpoint:
		u, err := base.Parse(ep)
		if err != nil || u.Host != base.Host || u.Scheme != base.Scheme {
			cancel()
			return nil, errors.New("sse: server advertised an endpoint on a different origin")
		}
		t.mu.Lock()
		t.post = u.String()
		t.mu.Unlock()
	case <-t.done:
		cancel()
		return nil, errors.New("sse: stream closed before the endpoint event")
	case <-ctx.Done():
		cancel()
		return nil, ctx.Err()
	}
	return t, nil
}

func (t *sseTransport) fail(err error) {
	t.once.Do(func() {
		t.mu.Lock()
		t.failed = err
		t.mu.Unlock()
		close(t.done)
	})
}

func (t *sseTransport) read(body io.ReadCloser) {
	defer body.Close()
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	event := ""
	var data bytes.Buffer
	flush := func() {
		defer func() { event = ""; data.Reset() }()
		if data.Len() == 0 {
			return
		}
		switch event {
		case "endpoint":
			select {
			case t.endpoint <- strings.TrimSpace(data.String()):
			default:
			}
		case "", "message":
			msg := append(json.RawMessage(nil), data.Bytes()...)
			select {
			case t.recv <- msg:
			case <-t.done:
			}
		}
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, ":"):
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	err := sc.Err()
	if err == nil {
		err = io.EOF
	}
	t.fail(err)
}

// Send POSTs one JSON-RPC message to the advertised endpoint.
func (t *sseTransport) Send(ctx context.Context, msg json.RawMessage) error {
	select {
	case <-t.done:
		return errSSEClosed
	default:
	}
	t.mu.Lock()
	target := t.post
	t.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(msg))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("sse: POST returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Receive returns the next message from the event stream.
func (t *sseTransport) Receive(ctx context.Context) (json.RawMessage, error) {
	select {
	case m := <-t.recv:
		return m, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-t.done:
		select {
		case m := <-t.recv:
			return m, nil
		default:
		}
		return nil, errSSEClosed
	}
}

// Close ends the stream.
func (t *sseTransport) Close() error {
	t.cancel()
	t.fail(errSSEClosed)
	return nil
}

// sseHTTPClient has no overall timeout because the stream is long-lived.
func sseHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, ResponseHeaderTimeout: 15 * time.Second, IdleConnTimeout: 90 * time.Second}}
}
