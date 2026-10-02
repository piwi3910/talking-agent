package telephony

import (
	"context"
	"encoding/binary"
	"io"

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
func captureLive(ctx context.Context, r io.Reader, pt uint8, start func() *pcmStream) {
	buf := make([]byte, 2048)
	var pre [][]byte
	var stream *pcmStream
	onset, silence, total := 0, 0, 0
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
		var energy int64
		for i := 0; i < len(pcm); i += 2 {
			v := int64(int16(binary.LittleEndian.Uint16(pcm[i:])))
			if v < 0 {
				v = -v
			}
			energy += v
		}
		voiced := energy/int64(n) > 450
		up := make([]byte, len(pcm)*2)
		for i := 0; i < len(pcm); i += 2 {
			copy(up[i*2:], pcm[i:i+2])
			copy(up[i*2+2:], pcm[i:i+2])
		}
		if stream == nil {
			pre = append(pre, up)
			for len(pre) > 1 && bufferedSamples(pre) > 2400 {
				pre = pre[1:]
			}
			if voiced {
				onset += n
			} else {
				onset = 0
			}
			if onset < 1024 {
				continue
			} // at least 128 ms of sustained speech
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
			continue
		}
		total += n
		if voiced {
			silence = 0
		} else {
			silence += n
		}
		if silence >= 5600 || total >= 8000*55 {
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
