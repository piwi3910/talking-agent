package telephony

import (
	"context"
	"encoding/binary"
	"io"

	"enterprise-ai-demo/internal/audio"

	"github.com/zaf/g711"
)

// pcmStream bounds buffered audio while ASR consumes the utterance as it arrives.
type pcmStream struct {
	ctx     context.Context
	chunks  chan []byte
	pending []byte
}

func newPCMStream(ctx context.Context) *pcmStream {
	return &pcmStream{ctx: ctx, chunks: make(chan []byte, 256)}
}
func (p *pcmStream) Read(b []byte) (int, error) {
	if len(b) == 0 {
		return 0, nil
	}
	if len(p.pending) == 0 {
		select {
		case <-p.ctx.Done():
			return 0, p.ctx.Err()
		case chunk, ok := <-p.chunks:
			if !ok {
				return 0, io.EOF
			}
			p.pending = chunk
		}
	}
	n := copy(b, p.pending)
	p.pending = p.pending[n:]
	return n, nil
}
func (p *pcmStream) push(b []byte) bool {
	select {
	case <-p.ctx.Done():
		return false
	case p.chunks <- b:
		return true
	default:
		return false
	}
}

// captureLive continuously reads RTP, including during replies. A credible speech
// onset starts live ASR and cancels the previous turn before recognition finishes.
// Speech is found by a spectral VAD against an adaptive noise floor (see
// audio.VAD): 128 ms of sustained speech is an onset, 700 ms of non-speech ends
// the utterance. The 8 kHz line audio is converted to 16 kHz for ASR with a
// polyphase interpolator rather than sample repetition.
func captureLive(ctx context.Context, r io.Reader, pt uint8, start func() *pcmStream) {
	const (
		onsetSamples   = 1024 // 128 ms of speech at 8 kHz
		silenceSamples = 5600 // 700 ms of non-speech
		maxSamples     = 8000 * 55
	)
	buf := make([]byte, 2048)
	var pre [][]byte
	var stream *pcmStream
	onset, silence, total := 0, 0, 0
	vad := audio.NewVAD()
	up8to16 := audio.Up8kTo16k()
	var samples, wide []int16
	defer func() {
		if stream != nil {
			close(stream.chunks)
		}
	}()
	for {
		n, err := r.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		pcm := g711.DecodeUlaw(buf[:n])
		if pt == 8 {
			pcm = g711.DecodeAlaw(buf[:n])
		}
		samples = samples[:0]
		for i := 0; i+1 < len(pcm); i += 2 {
			samples = append(samples, int16(binary.LittleEndian.Uint16(pcm[i:])))
		}
		vad.Process(samples, func(voiced bool) {
			switch {
			case voiced:
				onset += audio.VADFrame
				silence = 0
			default:
				onset = 0
				silence += audio.VADFrame
			}
		})
		wide = up8to16.Process(wide[:0], samples)
		up := make([]byte, len(wide)*2)
		for i, s := range wide {
			binary.LittleEndian.PutUint16(up[i*2:], uint16(s))
		}
		if stream == nil {
			pre = append(pre, up)
			for len(pre) > 1 && bufferedSamples(pre) > 2400 {
				pre = pre[1:]
			}
			if onset < onsetSamples {
				continue
			}
			stream = start()
			total = 0
			silence = 0
			for _, chunk := range pre {
				if !stream.push(chunk) {
					close(stream.chunks)
					stream = nil
					break
				}
				total += len(chunk) / 4
			}
			pre = nil
			onset = 0
			continue
		}
		if !stream.push(up) {
			close(stream.chunks)
			stream = nil
			pre = nil
			onset = 0
			continue
		}
		total += n
		if silence >= silenceSamples || total >= maxSamples {
			close(stream.chunks)
			stream = nil
			pre = nil
			onset = 0
			silence = 0
		}
	}
}
func bufferedSamples(pre [][]byte) int {
	n := 0
	for _, b := range pre {
		n += len(b) / 4
	}
	return n
}
