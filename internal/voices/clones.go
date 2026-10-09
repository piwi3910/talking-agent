package voices

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"enterprise-ai-demo/internal/speech"
)

const (
	cloneAudioFile = "reference.wav"
	cloneTextFile  = "reference.txt"
)

func (s *Store) cloneDir(id string) string { return filepath.Join(s.cloneRoot, id) }

// loadClones reads the reference files of the clones named in the settings file.
// A clone whose files are missing or unreadable is dropped with a warning:
// startup never fails over it. Entries with an invalid id are kept so that
// validation reports them, and the id is never used as a path.
func (s *Store) loadClones(saved []Custom) []Custom {
	var out []Custom
	for _, v := range saved {
		if v.Kind != KindClone || validID(v.ID) != nil {
			out = append(out, v)
			continue
		}
		ref, err := speech.LoadReference(s.cloneDir(v.ID), cloneAudioFile, cloneTextFile)
		if err == nil && ref == nil {
			err = fmt.Errorf("no reference files")
		}
		if err != nil {
			slog.Warn("dropping cloned voice whose reference files cannot be read", "voice", v.ID, "error", err)
			continue
		}
		s.clones[v.ID] = ref
		out = append(out, v)
	}
	return out
}

// checkKinds ensures a save neither creates a clone nor changes a voice's kind;
// clones only come from CreateClone.
func (s *Store) checkKinds(custom []Custom) error {
	for _, v := range custom {
		_, isClone := s.clones[v.ID]
		switch {
		case v.Kind == KindClone && !isClone:
			return fmt.Errorf("unknown cloned voice %q; create it with the clone flow", v.ID)
		case v.Kind != KindClone && isClone:
			return fmt.Errorf("voice %s is a cloned voice and cannot change kind", v.ID)
		}
	}
	return nil
}

// dropRemovedClones deletes the files of clones that a successful save left out.
// Callers hold the write lock.
func (s *Store) dropRemovedClones(kept []Custom) {
	keep := map[string]bool{}
	for _, v := range kept {
		keep[v.ID] = true
	}
	for id := range s.clones {
		if keep[id] {
			continue
		}
		delete(s.clones, id)
		if err := os.RemoveAll(s.cloneDir(id)); err != nil {
			slog.Warn("could not remove cloned voice files", "voice", id, "error", err)
		}
	}
}

// CloneRequest creates a clone from a recording and its exact transcript.
type CloneRequest struct {
	Revision   uint64
	ID         string
	Name       string
	Transcript string
	Audio      []byte
}

// CreateClone validates everything again, stores the reference atomically, then
// persists the settings. Memory changes only after both succeed.
func (s *Store) CreateClone(in CloneRequest) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if in.Revision != s.revision {
		return Snapshot{}, ErrConflict
	}
	in.Name = strings.TrimSpace(in.Name)
	if err := validID(in.ID); err != nil {
		return Snapshot{}, err
	}
	if s.hasVoice(s.custom, in.ID) {
		return Snapshot{}, fmt.Errorf("duplicate voice id %s", in.ID)
	}
	if err := validName(in.ID, in.Name); err != nil {
		return Snapshot{}, err
	}
	if len(s.clones) >= maxClones {
		return Snapshot{}, fmt.Errorf("at most %d cloned voices are allowed", maxClones)
	}
	wav, text, err := PrepareClone(in.Audio, in.Transcript)
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.writeClone(in.ID, wav, text); err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrStorage, err)
	}
	custom := append(append([]Custom{}, s.custom...), Custom{ID: in.ID, Name: in.Name, Kind: KindClone})
	raw, err := json.MarshalIndent(disk{Revision: s.revision + 1, Voices: custom, Personas: s.personas}, "", "  ")
	if err == nil {
		err = writeAtomic(s.path, raw)
	}
	if err != nil {
		os.RemoveAll(s.cloneDir(in.ID))
		return Snapshot{}, fmt.Errorf("%w: %w", ErrStorage, err)
	}
	s.revision++
	s.custom = custom
	s.clones[in.ID] = &speech.Reference{AudioBase64: base64.StdEncoding.EncodeToString(wav), Text: text}
	return s.snapshot(), nil
}

// writeClone stores the reference in a temp dir and renames it into place, so a
// clone directory is either complete or absent.
func (s *Store) writeClone(id string, wav []byte, text string) error {
	return s.writeReference(id, wav, text, nil)
}

// writeReference is writeClone plus extra small files (a design sample keeps the
// description it was rendered from).
func (s *Store) writeReference(id string, wav []byte, text string, extra map[string]string) error {
	if err := os.MkdirAll(s.cloneRoot, 0700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.cloneRoot, ".tmp-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err = os.WriteFile(filepath.Join(tmp, cloneAudioFile), wav, 0600); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(tmp, cloneTextFile), []byte(text), 0600); err != nil {
		return err
	}
	for name, content := range extra {
		if err = os.WriteFile(filepath.Join(tmp, name), []byte(content), 0600); err != nil {
			return err
		}
	}
	// Anything already at the final name is an orphan of a clone that is not in the settings.
	if err = os.RemoveAll(s.cloneDir(id)); err != nil {
		return err
	}
	return os.Rename(tmp, s.cloneDir(id))
}
