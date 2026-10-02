package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"enterprise-ai-demo/internal/config"
	"enterprise-ai-demo/internal/llm"
	"enterprise-ai-demo/internal/telemetry"
	"enterprise-ai-demo/internal/tools"
	"sync"
	"sync/atomic"
	"time"
)

type Pending struct {
	ID         string            `json:"id"`
	Tool       string            `json:"tool"`
	Arguments  map[string]string `json:"arguments"`
	Expires    time.Time         `json:"expires"`
	Original   string            `json:"-"`
	Definition tools.Definition  `json:"-"`
}
type Session struct {
	ID            string
	Agent         *config.Agent
	UserID        string
	Created       time.Time
	History       []llm.Message
	Active        map[string]bool
	Pending       map[string]Pending
	SafetyBlocked bool
	Events        *telemetry.Journal
	Busy          atomic.Bool
	cancelMu      sync.Mutex
	cancel        context.CancelFunc
}

func ID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Session) SetCancel(c context.CancelFunc) {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	s.cancel = c
}
func (s *Session) Cancel() {
	s.cancelMu.Lock()
	defer s.cancelMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
}

type Store struct {
	mu    sync.RWMutex
	items map[string]*Session
}

func NewStore() *Store { return &Store{items: map[string]*Session{}} }
func (s *Store) Create(a *config.Agent, user string) *Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, old := range s.items {
		if time.Since(old.Created) > 24*time.Hour && !old.Busy.Load() {
			delete(s.items, id)
		}
	}
	if len(s.items) >= 1000 {
		return nil
	}
	id := ID()
	x := &Session{ID: id, Agent: a, UserID: user, Created: time.Now(), Active: map[string]bool{}, Pending: map[string]Pending{}, Events: telemetry.New(id)}
	s.items[id] = x
	return x
}
func (s *Store) Get(id string) *Session { s.mu.RLock(); defer s.mu.RUnlock(); return s.items[id] }

// Delete releases a transport-owned session after its call worker finishes.
func (s *Store) Delete(id string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.items, id) }
