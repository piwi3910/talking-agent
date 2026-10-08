package telephony

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"sync"
	"time"

	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/audio"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/session"
	"enterprise-ai-demo/internal/speech"
	"github.com/emiago/diago"
	"github.com/emiago/diago/media"
	"github.com/emiago/sipgo"
	"github.com/zaf/g711"
)

type Server struct {
	Ready     func(*sipgo.UserAgent) error
	Stopping  func()
	Settings  *Settings
	Config    Config
	Agents    map[string]*config.Agent
	Sessions  *session.Store
	Runtime   *agent.Runtime
	Speech    *speech.Client
	Voices    map[string]*speech.Reference
	wg        sync.WaitGroup
	lifecycle sync.Mutex
	closing   bool
}

// Run owns SIP listeners and all active call workers until shutdown.
func (s *Server) Run(ctx context.Context) error {
	if err := s.Config.validate(s.Agents); err != nil {
		return err
	}
	if !s.Speech.Enabled() {
		return fmt.Errorf("SIP requires STT_URL and TTS_URL")
	}
	ua, err := sipgo.NewUA(sipgo.WithUserAgent("talking-agent"))
	if err != nil {
		return err
	}
	defer ua.Close()
	media.RTPPortStart = s.Config.RTPStart
	media.RTPPortEnd = s.Config.RTPEnd
	opts := []diago.DiagoOption{diago.WithMediaConfig(diago.MediaConfig{Codecs: []media.Codec{media.CodecAudioUlaw, media.CodecAudioAlaw}})}
	for _, transport := range []string{"udp", "tcp"} {
		opts = append(opts, diago.WithTransport(diago.Transport{Transport: transport, BindHost: s.Config.BindHost, BindPort: s.Config.Port, ExternalHost: s.Config.AdvertiseIP, ExternalPort: s.Config.Port, MediaExternalIP: net.ParseIP(s.Config.AdvertiseIP)}))
	}
	dg := diago.NewDiago(ua, opts...)
	slots := make(chan struct{}, s.Config.MaxCalls)
	err = dg.ServeBackground(ctx, func(d *diago.DialogServerSession) {
		s.lifecycle.Lock()
		if s.closing {
			s.lifecycle.Unlock()
			_ = d.Respond(503, "Service Unavailable", nil)
			return
		}
		s.wg.Add(1)
		s.lifecycle.Unlock()
		defer s.wg.Done()
		if !s.Config.allowed(d.InviteRequest.Source()) {
			_ = d.Respond(403, "Forbidden", nil)
			return
		}
		a := s.Agents[s.Config.Numbers[d.InviteRequest.Recipient.User]]
		profile := PersonaSettings{Cues: true}
		if s.Settings != nil {
			a, profile = s.Settings.Route(d.InviteRequest.Recipient.User)
		}
		if a == nil {
			_ = d.Respond(404, "Not Found", nil)
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
		default:
			_ = d.Respond(486, "Busy Here", nil)
			return
		}
		// The operator selects the account per persona; Caller-ID never selects identity.
		user := profile.UserID
		if user == "" {
			user = "sip-anonymous:" + session.ID()
		}
		sess := s.Sessions.Create(a, user)
		if sess == nil {
			_ = d.Respond(503, "Service Unavailable", nil)
			return
		}
		defer s.Sessions.Delete(sess.ID)
		callctx, cancel := context.WithTimeout(d.Context(), 30*time.Minute)
		defer cancel()
		stop := context.AfterFunc(ctx, cancel)
		defer stop()
		stopMedia := context.AfterFunc(callctx, func() { _ = d.Close() })
		defer stopMedia()
		defer func() { h, c := context.WithTimeout(context.Background(), 2*time.Second); defer c(); _ = d.Hangup(h) }()
		m, err := d.Answer(diago.AnswerOptions{})
		if err != nil {
			return
		}
		sess.Events.Emit("", "call.started", map[string]any{"transport": "sip", "number": d.InviteRequest.Recipient.User, "agent": a.ID})
		defer sess.Events.Emit("", "call.ended", map[string]any{"transport": "sip"})
		if err := s.converse(callctx, m, sess, profile); err != nil && callctx.Err() == nil {
			slog.Warn("SIP conversation ended", "session", sess.ID, "error", err)
		}
	})
	if err != nil {
		return err
	}
	if s.Ready != nil {
		if err := s.Ready(ua); err != nil {
			return err
		}
	}
	if s.Settings != nil {
		s.Settings.SetEnabled(true)
		defer s.Settings.SetEnabled(false)
	}
	slog.Info("SIP ready", "port", s.Config.Port, "routes", len(s.Config.Numbers), "max_calls", s.Config.MaxCalls)
	<-ctx.Done()
	if s.Stopping != nil {
		s.Stopping()
	}
	s.lifecycle.Lock()
	s.closing = true
	s.lifecycle.Unlock()
	s.wg.Wait()
	_ = ua.Close()
	return nil
}

// playback low-pass filters and decimates 24 kHz PCM into 8 kHz G.711 with a
// polyphase resampler, preserving arbitrary HTTP chunk boundaries.
func playback(ctx context.Context, w io.Writer, pt uint8, synth func(func([]byte) error) error) error {
	var pending, encoded []byte
	var samples, resampled []int16
	down := audio.Down24kTo8k()
	// The filter delays the signal by a fixed number of output samples. Dropping
	// that many at the start (and flushing as many at the end) keeps the output
	// time-aligned and exactly 1/3 of the input length, so no extra packet appears.
	delay := int(math.Round(down.Delay().Seconds() * 8000))
	skip := delay
	emitFrame := func(frame []byte) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, err := w.Write(frame)
		if err == nil && n != len(frame) {
			err = io.ErrShortWrite
		}
		return err
	}
	// encode turns 8 kHz samples into G.711 and emits every complete 20 ms frame.
	encode := func(pcm []int16) error {
		if skip > 0 {
			n := min(skip, len(pcm))
			skip -= n
			pcm = pcm[n:]
		}
		for _, sample := range pcm {
			b := g711.EncodeUlawFrame(sample)
			if pt == 8 {
				b = g711.EncodeAlawFrame(sample)
			}
			encoded = append(encoded, b)
			if len(encoded) == 160 {
				if err := emitFrame(encoded); err != nil {
					return err
				}
				encoded = encoded[:0]
			}
		}
		return nil
	}
	err := synth(func(chunk []byte) error {
		pending = append(pending, chunk...)
		samples = samples[:0]
		consumed := 0
		for ; consumed+2 <= len(pending); consumed += 2 {
			samples = append(samples, int16(binary.LittleEndian.Uint16(pending[consumed:])))
		}
		pending = append(pending[:0], pending[consumed:]...)
		resampled = down.Process(resampled[:0], samples)
		return encode(resampled)
	})
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("speech returned truncated PCM sample")
	}
	// Flush the filter tail (its group delay) so the last syllable is not clipped.
	if err := encode(down.Process(resampled[:0], make([]int16, delay*3))); err != nil {
		return err
	}
	if len(encoded) > 0 {
		silence := g711.EncodeUlawFrame(0)
		if pt == 8 {
			silence = g711.EncodeAlawFrame(0)
		}
		for len(encoded) < 160 {
			encoded = append(encoded, silence)
		}
		return emitFrame(encoded)
	}
	return nil
}
