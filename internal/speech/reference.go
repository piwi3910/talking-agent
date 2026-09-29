package speech

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reference is loaded once per persona and reused on every phrase request.
// A seed alone does not preserve a speaker when generating different text.
type Reference struct {
	AudioBase64 string
	Text        string
}

func LoadReference(dir, audio, transcript string) (*Reference, error) {
	if audio == "" && transcript == "" {
		return nil, nil
	}
	read := func(name string) ([]byte, error) {
		if !filepath.IsLocal(name) {
			return nil, fmt.Errorf("voice reference must be inside the agent directory")
		}
		p, err := filepath.EvalSymlinks(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		root, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		p, err = filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil || !filepath.IsLocal(rel) {
			return nil, fmt.Errorf("voice reference escapes agent directory")
		}
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.Size() > 2<<20 {
			return nil, fmt.Errorf("voice reference exceeds 2 MiB")
		}
		return os.ReadFile(p)
	}
	wav, err := read(audio)
	if err != nil {
		return nil, err
	}
	if len(wav) < 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, fmt.Errorf("voice reference must be a WAV")
	}
	text, err := read(transcript)
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(text))) == 0 || len(text) > 8000 {
		return nil, fmt.Errorf("invalid voice reference transcript")
	}
	return &Reference{AudioBase64: base64.StdEncoding.EncodeToString(wav), Text: strings.TrimSpace(string(text))}, nil
}
