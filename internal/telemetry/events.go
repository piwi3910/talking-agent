package telemetry

import (
	"log/slog"
	"sync"
	"time"
)

type Event struct {
	Version   int       `json:"version"`
	ID        uint64    `json:"id"`
	Type      string    `json:"type"`
	SessionID string    `json:"session_id"`
	TurnID    string    `json:"turn_id,omitempty"`
	Time      time.Time `json:"time"`
	Data      any       `json:"data"`
}
type Sink func(string, any)
type Journal struct {
	mu     sync.Mutex
	events []Event
	// Hook, when set before the first Emit, observes every event.
	Hook    func(Event)
	seq     uint64
	session string
}

func New(id string) *Journal { return &Journal{session: id} }
func (j *Journal) Emit(turn, kind string, data any) Event {
	e := j.emit(turn, kind, data)
	if j.Hook != nil {
		j.Hook(e)
	}
	return e
}
func (j *Journal) emit(turn, kind string, data any) Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	e := Event{1, j.seq, kind, j.session, turn, time.Now().UTC(), data}
	j.events = append(j.events, e)
	if len(j.events) > 1000 {
		j.events = j.events[len(j.events)-1000:]
	}
	slog.Info("agent_event", "type", kind, "session", j.session, "turn", turn)
	return e
}
func (j *Journal) Since(id uint64) []Event {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := []Event{}
	for _, e := range j.events {
		if e.ID > id {
			out = append(out, e)
		}
	}
	return out
}
