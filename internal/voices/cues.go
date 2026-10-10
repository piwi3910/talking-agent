package voices

import (
	"context"
	"encoding/json"
	"enterprise-ai-demo/internal/logx"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"enterprise-ai-demo/internal/speech"
)

// job fixes the voice and key at queue time, so the audio rendered under a key
// always matches it even if the persona's voice changes before the worker runs.
type job struct {
	key         string
	voice       speech.Voice
	state       string
	done, total int
	err         string
	force       bool
}

func (s *Store) voice(agentID string) speech.Voice {
	p := s.personas[agentID]
	return s.resolve(s.effectiveVoiceID(agentID), p.Direction)
}
func (s *Store) key(agentID string) string         { return s.cueKey(s.voice(agentID)) }
func (s *Store) setDir(agentID, key string) string { return filepath.Join(s.cueDir, agentID, key) }
func (s *Store) ready(agentID, key string) bool {
	_, err := os.Stat(filepath.Join(s.setDir(agentID, key), "cues.json"))
	return err == nil
}
func (s *Store) count(agentID string) int {
	var m struct{ Cues []json.RawMessage }
	_ = json.Unmarshal(s.manifest[agentID], &m)
	return len(m.Cues)
}

// needed reports whether the persona's current cue set has to be rendered.
func (s *Store) needed(agentID string) bool {
	// A voice still waiting for its sample renders once the sample exists.
	if id := s.personas[agentID].VoiceID; s.unavailable(id) || s.pending(id) {
		return false
	}
	return s.count(agentID) > 0 && !s.ready(agentID, s.key(agentID))
}
func (s *Store) status(agentID string) CueStatus {
	total := s.count(agentID)
	key := s.key(agentID)
	if j := s.jobs[agentID]; j != nil && j.key == key {
		return CueStatus{State: j.state, Done: j.done, Total: total, Error: j.err}
	}
	if s.ready(agentID, key) || total == 0 {
		return CueStatus{State: "ready", Done: total, Total: total}
	}
	return CueStatus{State: "queued", Total: total}
}

// CueDir returns the directory of the active cue set, or false while there is
// none: cues stay muted until a rendered set is complete. The cue wording ships
// with each agent, but the audio is always rendered in the persona's own voice.
func (s *Store) CueDir(agentID string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.agents[agentID] == nil {
		return "", false
	}
	key := s.key(agentID)
	return s.setDir(agentID, key), s.ready(agentID, key)
}

// enqueueMissing queues personas whose current set is missing, even when an
// older job for another key is still pending. A job for the current key is left
// alone, except that retry re-queues a finished (failed) one: saves retry, the
// worker's own follow-up pass does not, so a persistent failure cannot loop.
// Callers hold the write lock.
func (s *Store) enqueueMissing(retry bool) {
	for id := range s.agents {
		if !s.needed(id) {
			continue
		}
		if j := s.jobs[id]; j != nil && j.key == s.key(id) && (!retry || j.state == "queued" || j.state == "rendering") {
			continue
		}
		s.queueLocked(id, false)
	}
}
func (s *Store) queueLocked(id string, force bool) {
	v := s.voice(id)
	s.jobs[id] = &job{key: s.cueKey(v), voice: v, state: "queued", total: s.count(id), force: force}
	s.queue = append(s.queue, id)
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Force re-renders the persona's current cue set even if it exists.
func (s *Store) Force(agentID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.agents[agentID] == nil {
		return Snapshot{}, fmt.Errorf("unknown persona %s", agentID)
	}
	if s.count(agentID) == 0 {
		return Snapshot{}, fmt.Errorf("%s has no cue manifest", agentID)
	}
	if j := s.jobs[agentID]; j == nil || j.key != s.key(agentID) || (j.state != "queued" && j.state != "rendering") {
		s.queueLocked(agentID, true)
	}
	return s.snapshot(), nil
}

// Start queues missing sets and runs the single render worker until ctx ends.
func (s *Store) Start(ctx context.Context) {
	s.mu.Lock()
	missing := s.missingDesigns()
	presets := s.missingPresets()
	if len(missing) == 0 {
		s.enqueueMissing(false)
	}
	s.mu.Unlock()
	if len(presets) > 0 {
		go s.regenPresets(ctx, presets)
	}
	if len(missing) > 0 {
		// Design voices lost their sample (qwen3): regenerate, then queue cue sets.
		go s.regenDesigns(ctx, missing)
	}
	go func() {
		for {
			for {
				id, j := s.next()
				if j == nil {
					break
				}
				s.render(ctx, id, j)
				if ctx.Err() != nil {
					return
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
			}
		}
	}()
}
func (s *Store) next() (string, *job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.queue) > 0 {
		id := s.queue[0]
		s.queue = s.queue[1:]
		if j := s.jobs[id]; j != nil && j.state == "queued" {
			j.state = "rendering"
			return id, j
		}
	}
	return "", nil
}

// trimError returns a short message that is safe to show in the UI: no URLs,
// hosts or file paths. The full error is logged by the caller.
func trimError(err error) string {
	var urlErr *url.Error
	var netErr net.Error
	var pathErr *os.PathError
	switch {
	case errors.As(err, &urlErr), errors.As(err, &netErr), errors.Is(err, context.DeadlineExceeded):
		return "speech service unavailable"
	case errors.As(err, &pathErr):
		return "could not write cue files"
	}
	msg := strings.Join(strings.Fields(logx.Error(err)), " ")
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

// render synthesizes every baked cue sequentially into a temp dir and renames
// it into place only when complete.
// It renders exactly the voice fixed in the job under the job's own key.
func (s *Store) render(ctx context.Context, agentID string, j *job) {
	key, force, voice := j.key, j.force, j.voice
	raw := s.manifest[agentID] // never modified after Open
	err := func() error {
		if !force && s.ready(agentID, key) {
			return nil
		}
		if !s.tts.Enabled() {
			return fmt.Errorf("speech is not configured")
		}
		var m struct {
			ReferenceSHA string `json:"reference_sha256"`
			Cues         []struct {
				ID       string `json:"id"`
				Category string `json:"category"`
				Text     string `json:"text"`
				Duration int    `json:"duration_ms"`
			} `json:"cues"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return err
		}
		root := filepath.Join(s.cueDir, agentID)
		if err := os.MkdirAll(root, 0700); err != nil {
			return err
		}
		tmp, err := os.MkdirTemp(root, ".tmp-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		if err = os.Mkdir(filepath.Join(tmp, "cues"), 0700); err != nil {
			return err
		}
		for i := range m.Cues {
			c := &m.Cues[i]
			var pcm []byte
			// Background renders never delay a caller: wait while live speech runs.
			if err = s.tts.WaitIdle(ctx); err != nil {
				return err
			}
			cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
			err = s.tts.SynthesizeBackground(cctx, speech.Spoken(c.Text), voice, func(b []byte) error { pcm = append(pcm, b...); return nil })
			cancel()
			if err != nil {
				return err
			}
			if len(pcm)%2 != 0 {
				return fmt.Errorf("invalid PCM for %s", c.ID)
			}
			c.Duration = len(pcm) / 48
			if err = os.WriteFile(filepath.Join(tmp, "cues", c.ID+".wav"), speech.WAV(pcm), 0600); err != nil {
				return err
			}
			s.mu.Lock()
			if cur := s.jobs[agentID]; cur == j {
				j.done = i + 1
			}
			s.mu.Unlock()
		}
		// The browser compares this whole string as a cache-buster; the render
		// generation makes a forced re-render of the same key look new.
		m.ReferenceSHA = fmt.Sprintf("%s-%d", key, time.Now().UnixNano())
		out, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			return err
		}
		if err = os.WriteFile(filepath.Join(tmp, "cues.json"), out, 0600); err != nil {
			return err
		}
		final := s.setDir(agentID, key)
		// Keep the old set in place until the new one is renamed in, so readers
		// never see a gap: move it aside, rename in, then delete the aside copy.
		aside := ""
		if _, statErr := os.Stat(final); statErr == nil {
			aside = filepath.Join(root, fmt.Sprintf(".old-%s-%d", key, time.Now().UnixNano()))
			if err = os.Rename(final, aside); err != nil {
				return err
			}
		}
		if err = os.Rename(tmp, final); err != nil {
			if aside != "" {
				_ = os.Rename(aside, final)
			}
			return err
		}
		if aside != "" {
			os.RemoveAll(aside)
		}
		// Superseded sets are dropped, but only if this render is still the current voice.
		s.mu.RLock()
		current := s.key(agentID) == key
		s.mu.RUnlock()
		if current {
			old, _ := filepath.Glob(filepath.Join(root, "*"))
			for _, d := range old {
				if filepath.Base(d) != key {
					os.RemoveAll(d)
				}
			}
		}
		return nil
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		slog.Warn("voice cue render failed", "agent", agentID, "error", err)
		j.state, j.err = "failed", trimError(err)
		if ctx.Err() != nil {
			j.err = "stopped"
		}
	} else {
		j.state, j.done = "ready", j.total
	}
	// The voice may have changed while rendering; pick up the new target.
	s.enqueueMissing(false)
}
