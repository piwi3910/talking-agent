package knowledge

import (
	"context"
	"enterprise-ai-demo/internal/config"
	"os"
	"path/filepath"
	"strings"
)

type Provider interface {
	Retrieve(context.Context, *config.Agent, string) (string, error)
}
type Local struct{}

// Phase 1 catalogs are deliberately small. The interface can later rank documents.
func (Local) Retrieve(ctx context.Context, a *config.Agent, query string) (string, error) {
	var b strings.Builder
	for _, p := range a.Knowledge {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		raw, err := os.ReadFile(filepath.Join(a.Dir, p))
		if err != nil {
			return "", err
		}
		b.WriteString("\nSource: " + p + "\n")
		b.Write(raw)
		if b.Len() > 24000 {
			break
		}
	}
	return b.String(), nil
}
