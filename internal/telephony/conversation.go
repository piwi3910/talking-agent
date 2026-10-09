package telephony

import (
	"context"
	"errors"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"github.com/emiago/diago"
)

type voiceJob struct {
	ctx      context.Context
	cancel   context.CancelFunc
	id       string
	audio    io.Reader
	greeting bool
}
type approval struct {
	id    string
	ready bool
}

var punctuation = regexp.MustCompile(`[.!?](?:\s|$)`)
var technicalID = regexp.MustCompile(`\b(?:Related )?ID:\s*[^\s·,]+`)
var stopWords = regexp.MustCompile(`^(?:please )?(?:stop|stop talking|stop speaking|be quiet|pause)(?: please)?$`)

func normalized(text string) string {
	return strings.Trim(strings.ToLower(strings.TrimSpace(text)), ".!?")
}

func (s *Server) converse(parent context.Context, m *diago.DialogMedia, sess *session.Session, profile PersonaSettings) error {
	var props diago.MediaProps
	r, err := m.AudioReader(diago.WithAudioReaderMediaProps(&props))
	if err != nil {
		return err
	}
	w, err := m.AudioWriter()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	jobs := make(chan voiceJob, 4)
	var mu sync.Mutex
	var active context.CancelFunc
	newJob := func() voiceJob {
		mu.Lock()
		defer mu.Unlock()
		if active != nil {
			active()
		}
		c, stop := context.WithTimeout(ctx, 2*time.Minute)
		active = stop
		return voiceJob{ctx: c, cancel: stop, id: session.ID()}
	}
	first := newJob()
	first.greeting = true
	jobs <- first
	captureDone := make(chan struct{})
	go func() {
		defer close(captureDone)
		defer cancel()
		captureLive(ctx, r, props.Codec.PayloadType, func() *pcmStream {
			job := newJob()
			stream := newPCMStream(job.ctx)
			job.audio = stream
			sess.Events.Emit(job.id, "voice.interrupted", map[string]any{"transport": "sip"})
			select {
			case jobs <- job:
			case <-ctx.Done():
				job.cancel()
			}
			return stream
		})
	}()
	defer func() {
		cancel()
		mu.Lock()
		if active != nil {
			active()
		}
		mu.Unlock()
		_ = m.Close()
		<-captureDone
	}()
	p := approval{}
	output, cues := true, profile.Cues
	say := func(c context.Context, text, turn string) error {
		if !output {
			return nil
		}
		return s.sayPhone(c, w, props.Codec.PayloadType, sess, text, turn)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case job := <-jobs:
			if job.ctx.Err() != nil {
				job.cancel()
				continue
			}
			if job.greeting {
				// Personas with persona.opening speak first through a real turn
				// (memory, tools) instead of the static welcome text.
				if opening, ok := agent.OpeningTurn(sess.Agent, ""); ok {
					sess.Events.Emit(job.id, "turn.started", map[string]any{"opening": true, "transport": "sip"})
					sess.Busy.Store(true)
					sess.SetCancel(job.cancel)
					if err := s.runPhoneTurn(job.ctx, w, props.Codec.PayloadType, sess, job.id, opening, output, cues); err != nil && job.ctx.Err() == nil {
						sess.Events.Emit(job.id, "tts.failed", map[string]any{"transport": "sip"})
					}
					job.cancel()
					continue
				}
				greeting := sess.Agent.Branding["welcome"]
				if greeting == "" {
					greeting = "Hello, you have reached " + sess.Agent.Name + ". How can I help you?"
				}
				_ = say(job.ctx, greeting, job.id)
				job.cancel()
				continue
			}
			started := time.Now()
			text := ""
			sess.Events.Emit(job.id, "stt.started", map[string]any{"sample_rate": 16000, "transport": "sip"})
			err := s.Speech.Transcribe(job.ctx, job.audio, func(part string, final bool) error {
				kind := "stt.partial"
				if final {
					kind = "stt.final"
					text = part
				}
				sess.Events.Emit(job.id, kind, map[string]any{"text": part, "duration_ms": time.Since(started).Milliseconds()})
				return nil
			})
			if job.ctx.Err() != nil {
				job.cancel()
				continue
			}
			if err != nil {
				sess.Events.Emit(job.id, "stt.failed", map[string]any{"transport": "sip"})
				_ = say(job.ctx, "I could not hear that clearly. Please try again.", job.id)
				job.cancel()
				continue
			}
			text = strings.TrimSpace(text)
			command := normalized(text)
			if text == "" {
				job.cancel()
				continue
			}
			if stopWords.MatchString(command) {
				if cues {
					_ = s.playCue(job.ctx, w, props.Codec.PayloadType, sess, "interrupted", job.id)
				}
				job.cancel()
				continue
			}
			switch command {
			case "turn off acknowledgements", "turn off acknowledgments", "disable acknowledgements", "disable acknowledgments":
				cues = false
				_ = say(job.ctx, "Voice acknowledgements are off.", job.id)
				job.cancel()
				continue
			case "turn on acknowledgements", "turn on acknowledgments", "enable acknowledgements", "enable acknowledgments":
				cues = true
				_ = say(job.ctx, "Voice acknowledgements are on.", job.id)
				job.cancel()
				continue
			case "stop audio", "turn off spoken replies":
				_ = say(job.ctx, "Spoken replies are off. Say speak replies to turn them on.", job.id)
				output = false
				job.cancel()
				continue
			case "speak replies", "turn on spoken replies":
				output = true
				_ = say(job.ctx, "Spoken replies are on.", job.id)
				job.cancel()
				continue
			}
			turn := agent.Turn{Text: text}
			confirm := command == "confirm" || command == "i confirm" || command == "confirm the action" || command == "yes confirm"
			reject := command == "cancel" || command == "cancel the action" || command == "reject" || command == "no cancel"
			if confirm || reject {
				if p.id == "" && len(sess.Pending) > 0 {
					ids := []string{}
					for id := range sess.Pending {
						ids = append(ids, id)
					}
					sort.Strings(ids)
					p = approval{id: ids[0]}
				}
				if p.id == "" {
					_ = say(job.ctx, "There is no action waiting for approval.", job.id)
					job.cancel()
					continue
				}
				pending, ok := sess.Pending[p.id]
				if !ok || time.Now().After(pending.Expires) {
					p = approval{}
					_ = say(job.ctx, "That action has expired. Please request it again.", job.id)
					job.cancel()
					continue
				}
				if !p.ready && !reject {
					// Approval always requires an audible proposal, even if ordinary replies were muted.
					output = true
					if err := say(job.ctx, proposalText(pending), job.id); err == nil && job.ctx.Err() == nil {
						p.ready = true
					}
					job.cancel()
					continue
				}
				turn = agent.Turn{Confirmation: p.id, Reject: reject}
				p = approval{}
			} else {
				p = approval{}
			}
			sess.Events.Emit(job.id, "turn.started", map[string]any{"message": text, "transport": "sip"})
			sess.Busy.Store(true)
			sess.SetCancel(job.cancel)
			if err := s.runPhoneTurn(job.ctx, w, props.Codec.PayloadType, sess, job.id, turn, output, cues); err != nil && job.ctx.Err() == nil {
				sess.Events.Emit(job.id, "tts.failed", map[string]any{"transport": "sip"})
				_ = say(job.ctx, "The spoken reply failed. Please try again.", job.id)
			}
			if job.ctx.Err() == nil && len(sess.Pending) > 0 {
				ids := make([]string, 0, len(sess.Pending))
				for id := range sess.Pending {
					ids = append(ids, id)
				}
				sort.Strings(ids)
				// One exact pending action is presented at a time. Never approve a batch implicitly.
				p = approval{id: ids[0]}
				if output {
					if err := say(job.ctx, proposalText(sess.Pending[p.id]), job.id); err == nil && job.ctx.Err() == nil {
						p.ready = true
					}
				}
			}
			job.cancel()
		}
	}
}

func proposalText(p session.Pending) string {
	labels := map[string]string{"wifi.optimize": "update your Wi-Fi settings", "wifi.restart": "restart your router", "plan.change": "change your plan", "appointment.book": "book an appointment", "appointment.reschedule": "reschedule an appointment", "appointment.cancel": "cancel an appointment", "technician.book": "book a technician visit", "ticket.create": "create a support ticket", "ticket.update": "update a support ticket", "support.create_request": "send your service request"}
	label := labels[p.Tool]
	if label == "" {
		label = strings.ReplaceAll(p.Tool, ".", " ")
	}
	keys := make([]string, 0, len(p.Arguments))
	for k := range p.Arguments {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	text := "Please review this action: " + label + ". "
	for _, key := range keys {
		text += strings.ReplaceAll(key, "_", " ") + ": " + p.Arguments[key] + ". "
	}
	return text + "Say confirm to approve this exact action, or cancel to reject it."
}

func (s *Server) sayPhone(ctx context.Context, w io.Writer, pt uint8, sess *session.Session, text, turn string) error {
	voice := s.Voices.Resolve(sess.Agent.ID)
	text = speech.Spoken(text)
	if text == "" {
		return nil
	}
	sess.Events.Emit(turn, "tts.started", map[string]any{"characters": len(text), "reference_voice": voice.Reference != nil, "transport": "sip"})
	first := true
	begin := time.Now()
	err := playback(ctx, w, pt, func(emit func([]byte) error) error {
		return s.Speech.Synthesize(ctx, text, voice, func(chunk []byte) error {
			if first {
				first = false
				sess.Events.Emit(turn, "tts.first_audio", map[string]any{"ttfa_ms": time.Since(begin).Milliseconds(), "transport": "sip"})
			}
			return emit(chunk)
		})
	})
	if err == nil {
		sess.Events.Emit(turn, "tts.completed", map[string]any{"duration_ms": time.Since(begin).Milliseconds(), "transport": "sip"})
	}
	return err
}

// runPhoneTurn keeps the runtime single-threaded for this session while a bounded
// speech worker consumes sentence chunks and optional cues from the event journal.
func (s *Server) runPhoneTurn(ctx context.Context, w io.Writer, pt uint8, sess *session.Session, turnID string, turn agent.Turn, output, cues bool) error {
	runctx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	var before uint64
	for _, e := range sess.Events.Since(0) {
		before = e.ID
	}
	go func() { defer close(done); s.Runtime.Run(runctx, sess, turnID, turn) }()
	defer func() { cancel(); <-done }()
	// Only this worker writes RTP; no cue can overlap or overtake answer speech.
	text := ""
	spoken := false
	failed := false
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	cueAt := time.Now().Add(1200 * time.Millisecond)
	cueCount := 0
	category := "waiting"
	finished := false
	flush := func(final bool) error {
		for strings.TrimSpace(text) != "" {
			n := 0
			if loc := punctuation.FindStringIndex(text); loc != nil {
				n = loc[1]
			}
			if n == 0 && len(text) > 240 {
				n = strings.LastIndex(text[:240], " ") + 1
				if n == 0 {
					n = 240
				}
			}
			if n == 0 && final {
				n = len(text)
			}
			if n == 0 {
				return nil
			}
			phrase := spokenText(text[:n])
			text = text[n:]
			if phrase != "" && output {
				spoken = true
				if err := s.sayPhone(runctx, w, pt, sess, phrase, turnID); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for {
		for _, e := range sess.Events.Since(before) {
			before = e.ID
			if e.TurnID != turnID {
				continue
			}
			switch e.Type {
			case "agent.response.delta":
				if data, ok := e.Data.(map[string]any); ok {
					delta, _ := data["text"].(string)
					text += delta
				}
			case "agent.response.completed":
				if text == "" && !spoken {
					if data, ok := e.Data.(map[string]any); ok {
						text, _ = data["text"].(string)
					}
				}
			case "tool.started":
				category = "lookup"
				if data, ok := e.Data.(map[string]any); ok {
					tool, _ := data["tool"].(string)
					if strings.HasPrefix(tool, "network.") || strings.HasPrefix(tool, "wifi.") {
						category = "network"
					}
					if tool == "appointment.availability" || tool == "technician.availability" {
						category = "availability"
					}
				}
			case "tool.completed", "tool.failed":
				category = "waiting"
			case "action.confirmation.required", "safety.blocked":
				cues = false
			case "agent.error":
				failed = true
				cues = false
			case "turn.completed":
				finished = true
			}
		}
		if err := flush(finished); err != nil {
			return err
		}
		if finished {
			if failed && !spoken && output {
				return s.sayPhone(runctx, w, pt, sess, "I could not complete that request. Please try again.", turnID)
			}
			return nil
		}
		if output && cues && !spoken && cueCount < 2 && time.Now().After(cueAt) {
			err := s.playCue(runctx, w, pt, sess, category, turnID)
			if errors.Is(err, context.Canceled) {
				return err
			}
			cueCount++
			cueAt = time.Now().Add(8 * time.Second)
		}
		select {
		case <-runctx.Done():
			return runctx.Err()
		case <-tick.C:
		}
	}
}
func spokenText(text string) string {
	// Unlike the web, callers cannot look at a visual list. Read all options, bounded
	// into sentence-sized chunks, and retain exact IDs in action approval prompts.
	text = technicalID.ReplaceAllString(text, "")
	text = strings.NewReplacer("*", "", "#", "", "`", "", " · ", ", ", "\n- ", ". ").Replace(text)
	return strings.TrimSpace(text)
}
