package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/dereknguyen269/jev-harness/internal/domain"
)

// CacheKey hashes canonical tool + normalized args + relevant context.
// Destructive commands must still not be cached (see shouldCache).
func CacheKey(n domain.NormalizedRequest) string {
	var sb strings.Builder
	sb.WriteString(string(n.Canonical))
	sb.WriteString("|")
	sb.WriteString(n.Command)
	sb.WriteString("|")
	sb.WriteString(n.Path)
	sb.WriteString("|")
	sb.WriteString(n.URL)
	sb.WriteString("|")
	sb.WriteString(n.Resource)
	sb.WriteString("|")
	sb.WriteString(n.Request.Context.Environment)
	sb.WriteString("|")
	sb.WriteString(n.Request.Context.Workspace)
	sb.WriteString("|")
	sb.WriteString(n.Request.Context.WorkingDir)
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}
