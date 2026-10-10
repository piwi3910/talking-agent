package api

import (
	"context"
	"enterprise-ai-demo/internal/agent"
	"enterprise-ai-demo/internal/session"
	"net/http"
	"time"
)

// openingRoutes lets a persona with persona.opening speak first.
func (a *API) openingRoutes(m *http.ServeMux) {
	m.HandleFunc("POST /api/sessions/{id}/open", a.openSession)
}

// contactName resolves the selected identity's display name for the opening
// instruction and memory query. A failed lookup only loses that polish.
func (a *API) contactName(ctx context.Context, s *session.Session) string {
	users, err := a.users(ctx, s.Agent, false)
	if err != nil {
		return ""
	}
	for _, u := range users {
		if u.ID == s.UserID {
			return u.Name
		}
	}
	return ""
}

// openSession starts the opening turn: 202 on success, 404 for an unknown
// session, 400 when the persona has no persona.opening, and 409 when the
// session already has turns or an active turn. The internal instruction is
// never stored in history, emitted, or shown as a user message.
func (a *API) openSession(w http.ResponseWriter, r *http.Request) {
	s := a.Sessions.Get(r.PathValue("id"))
	if s == nil {
		fail(w, 404, "Session not found")
		return
	}
	turn, ok := agent.OpeningTurn(s.Agent, a.contactName(r.Context(), s))
	if !ok {
		fail(w, 400, "This persona does not speak first")
		return
	}
	if !s.Busy.CompareAndSwap(false, true) {
		fail(w, 409, "A turn is already running")
		return
	}
	// Busy is held, so History is stable. Any earlier turn means the conversation has begun.
	if len(s.History) > 0 {
		s.Busy.Store(false)
		fail(w, 409, "The conversation has already started")
		return
	}
	ctx, cancel := context.WithTimeout(a.Root, 2*time.Minute)
	s.SetCancel(cancel)
	id := session.ID()
	s.Events.Emit(id, "turn.started", map[string]any{"opening": true})
	a.Workers.Add(1)
	go func() {
		defer a.Workers.Done()
		defer cancel()
		a.Runtime.Run(ctx, s, id, turn)
	}()
	write(w, 202, map[string]string{"turn_id": id})
}
