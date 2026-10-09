package agent

import (
	"context"
	"enterprise-ai-demo/internal/memory"
	"enterprise-ai-demo/internal/session"
	"strings"
	"testing"
	"time"
)

func TestSchoolRuntimeAndMemoryIsolation(t *testing.T) {
	r, agents, _ := setup(t)
	store := session.NewStore()
	s := store.Create(agents["school-services"], "F001")
	r.Run(context.Background(), s, "school-1", Turn{Text: "Show school tour availability"})
	if !strings.Contains(conversation(s), "Which day suits you best") || strings.Contains(conversation(s), "TOUR-") || strings.Contains(conversation(s), "UTC") {
		t.Fatal(conversation(s))
	}
	r.Run(context.Background(), s, "school-2", Turn{Text: "Mornings normally work better for me."})
	deadline := time.Now().Add(time.Second)
	for {
		own, err := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(s), Query: "tour"})
		if err != nil {
			t.Fatal(err)
		}
		if len(own) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("school preference not stored")
		}
		time.Sleep(time.Millisecond)
	}
	for _, tc := range []struct{ agent, user string }{{"school-services", "F002"}, {"telecom-support", "F001"}, {"aquila-reception", "F001"}} {
		other := store.Create(agents[tc.agent], tc.user)
		got, err := r.Memory.Retrieve(context.Background(), memory.RetrieveRequest{Scope: Scope(other), Query: "tour"})
		if err != nil || len(got) != 0 {
			t.Fatalf("school memory leaked to %+v", tc)
		}
	}
}
