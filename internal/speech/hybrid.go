package speech

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"
)

const (
	defaultFinalTimeout = 1500 * time.Millisecond
	// finalWindowBytes is the audio the final model decodes in one pass (30 s of
	// 16 kHz s16le). Longer utterances keep the streaming recognizer's final.
	finalWindowBytes = 30 * 16000 * 2
)

// HybridSTT reports whether a whole-utterance final recognizer is configured.
func (c *Client) HybridSTT() bool {
	return c != nil && c.FinalURL != "" && c.FinalModel != ""
}

// STTLabel names the recognizers for the /api/voice description.
func (c *Client) STTLabel() string {
	if c.HybridSTT() {
		return "Nemotron 3.5 ASR (live) + Qwen3-ASR 1.7B (final)"
	}
	if strings.HasPrefix(c.sttModel(), "qwen3-asr-1.7b") {
		return "Qwen3-ASR 1.7B"
	}
	return "Nemotron 3.5 ASR"
}

// queue is an unbounded, never-blocking writer with a blocking reader, so a slow
// or dead final recognizer can never stall the live one.
type queue struct {
	mu        sync.Mutex
	cond      *sync.Cond
	chunks    [][]byte
	closed    bool
	abandoned bool
	err       error
}

func newQueue() *queue {
	q := &queue{}
	q.cond = sync.NewCond(&q.mu)
	return q
}

func (q *queue) write(p []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.abandoned || q.closed {
		return
	}
	q.chunks = append(q.chunks, append([]byte(nil), p...))
	q.cond.Broadcast()
}

func (q *queue) close(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed, q.err = true, err
	q.cond.Broadcast()
}

func (q *queue) abandon() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.abandoned, q.chunks = true, nil
	q.cond.Broadcast()
}

func (q *queue) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.chunks) == 0 && !q.closed && !q.abandoned {
		q.cond.Wait()
	}
	if len(q.chunks) > 0 {
		n := copy(p, q.chunks[0])
		if n < len(q.chunks[0]) {
			q.chunks[0] = q.chunks[0][n:]
		} else {
			q.chunks = q.chunks[1:]
		}
		return n, nil
	}
	if q.abandoned {
		return 0, io.ErrClosedPipe
	}
	if q.err != nil {
		return 0, q.err
	}
	return 0, io.EOF
}

type finalResult struct {
	text   string
	err    error
	doneAt time.Time
}

// transcribeHybrid streams the same PCM to the live recognizer and the final
// recognizer. The live recognizer's partials are relayed as they arrive; the
// final is the final recognizer's text when it lands in time, otherwise the
// live recognizer's own final.
func (c *Client) transcribeHybrid(ctx context.Context, audio io.Reader, emit func(string, bool) error) error {
	timeout := c.FinalTimeout
	if timeout <= 0 {
		timeout = defaultFinalTimeout
	}
	fctx, cancelFinal := context.WithCancel(ctx)
	defer cancelFinal()
	liveR, liveW := io.Pipe()
	defer liveR.Close()
	q := newQueue()

	var mu sync.Mutex
	var finishedAt time.Time
	var total int
	trace(ctx, "stt.final.request", map[string]any{"model": c.FinalModel})
	results := make(chan finalResult, 1)
	go func() {
		var text string
		err := c.transcribeLive(fctx, c.FinalURL, c.FinalModel, "English", q, func(t string, final bool) error {
			if final {
				text = t
			}
			return nil
		})
		q.abandon()
		results <- finalResult{text: text, err: err, doneAt: time.Now()}
	}()

	go func() {
		buf := make([]byte, 8192)
		liveDead := false
		for {
			n, err := audio.Read(buf)
			if n > 0 {
				mu.Lock()
				total += n
				mu.Unlock()
				q.write(buf[:n])
				if !liveDead {
					// A dead live recognizer must not starve the final one.
					if _, werr := liveW.Write(buf[:n]); werr != nil {
						liveDead = true
					}
				}
			}
			if err != nil {
				mu.Lock()
				finishedAt = time.Now()
				tooLong := total > finalWindowBytes
				mu.Unlock()
				if err == io.EOF {
					liveW.Close()
					q.close(nil)
					if tooLong {
						cancelFinal()
					}
				} else {
					liveW.CloseWithError(err)
					q.close(err)
				}
				return
			}
		}
	}()

	var liveText string
	var liveFinal bool
	err := c.transcribeLive(ctx, c.STTURL, c.sttModel(), c.sttLanguage(), liveR, func(t string, final bool) error {
		if final {
			liveText, liveFinal = t, true
			return nil
		}
		return emit(t, false)
	})
	liveR.Close() // never leave the copier blocked on a dead live recognizer
	if ctx.Err() != nil {
		return ctx.Err()
	}
	mu.Lock()
	end, bytes := finishedAt, total
	mu.Unlock()
	if end.IsZero() {
		end = time.Now()
	}
	liveOK := err == nil && liveFinal

	var res finalResult
	reason := ""
	if bytes > finalWindowBytes {
		reason = "utterance_too_long"
	} else {
		select {
		case res = <-results:
		default:
			wait := time.NewTimer(time.Until(end.Add(timeout)))
			select {
			case res = <-results:
			case <-wait.C:
				reason = "timeout"
			case <-ctx.Done():
			}
			wait.Stop()
		}
		if reason == "" {
			switch {
			case res.err != nil:
				reason = "error"
			case strings.TrimSpace(res.text) == "":
				reason = "empty"
			}
		}
	}
	if reason == "" {
		trace(ctx, "stt.final.done", map[string]any{"model": c.FinalModel, "latency_ms": max(0, res.doneAt.Sub(end).Milliseconds()), "chars": len(res.text), "live_chars": len(liveText), "differs": strings.TrimSpace(res.text) != strings.TrimSpace(liveText)})
		return emit(res.text, true)
	}
	data := map[string]any{"model": c.FinalModel, "reason": reason, "live_ok": liveOK}
	if res.err != nil {
		data["error"] = res.err.Error()
	}
	trace(ctx, "stt.final.fallback", data)
	if !liveOK {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	return emit(liveText, true)
}
