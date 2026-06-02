// Package inbox implements ports.AgentMessenger by writing each message as a
// file in <session-workspace>/.ao/inbox/. The agent reads its inbox on demand;
// pinging the runtime pane to consume new files is a separate concern that
// lives in the runtime adapter, not here.
package inbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SessionWorkspace resolves a session id to the absolute path of its workspace.
// The sqlite store satisfies this via GetSession; the adapter is in
// daemon/session_wiring.go.
type SessionWorkspace interface {
	WorkspacePath(ctx context.Context, id domain.SessionID) (string, error)
}

// Messenger writes inbox files into per-session workspaces.
type Messenger struct {
	lookup SessionWorkspace
	clock  func() time.Time
}

// New builds a Messenger over the given workspace lookup. lookup is required.
func New(lookup SessionWorkspace) *Messenger {
	return &Messenger{lookup: lookup, clock: time.Now}
}

var _ ports.AgentMessenger = (*Messenger)(nil)

// Send writes message into <workspace>/.ao/inbox/<unix-nano>_<sha256-prefix>.md.
//
// Filename collisions are practically impossible: nanosecond timestamp plus an
// 8-char hash of the body. We do not retry on EEXIST.
//
// Symlink safety: if .ao or .ao/inbox already exists as a symlink, refuse.
// Otherwise os.MkdirAll creates real directories and os.WriteFile (which uses
// O_CREATE|O_WRONLY|O_TRUNC without O_NOFOLLOW) writes the message body. The
// inbox is owned by ao; a symlink there is either user misconfig or attack.
func (m *Messenger) Send(ctx context.Context, id domain.SessionID, message string) error {
	ws, err := m.lookup.WorkspacePath(ctx, id)
	if err != nil {
		return fmt.Errorf("inbox: lookup workspace for %s: %w", id, err)
	}
	if ws == "" {
		return fmt.Errorf("inbox: empty workspace path for %s", id)
	}

	aoDir := filepath.Join(ws, ".ao")
	if err := ensureRealDir(aoDir); err != nil {
		return fmt.Errorf("inbox: prepare .ao for %s: %w", id, err)
	}
	inboxDir := filepath.Join(aoDir, "inbox")
	if err := ensureRealDir(inboxDir); err != nil {
		return fmt.Errorf("inbox: prepare inbox for %s: %w", id, err)
	}

	name := FilenameFor(TimeFromContext(ctx, m.clock), message)
	if err := os.WriteFile(filepath.Join(inboxDir, name), []byte(message), 0o600); err != nil {
		return fmt.Errorf("inbox: write %s for %s: %w", name, id, err)
	}
	return nil
}

// ensureRealDir creates path if missing (0755), refuses if path is a symlink.
// Lstat (not Stat) is used so a symlink isn't followed into a different tree.
//
// The workspace root itself is not Lstat-checked because gitworktree.Workspace
// resolves ManagedRoot to an absolute, symlink-free path at construction
// (gitworktree.physicalAbs); per-session workspaces under it are created by ao.
// A symlinked .ao or .ao/inbox inside an ao-owned workspace would be user
// misconfig or attack, and is the only segment that can be tampered with
// between Spawn and Send.
func ensureRealDir(path string) error {
	info, err := os.Lstat(path)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q is a symlink; refusing to follow", path)
		}
		if !info.IsDir() {
			return fmt.Errorf("%q exists and is not a directory", path)
		}
		return nil
	case errors.Is(err, os.ErrNotExist):
		return os.MkdirAll(path, 0o750)
	default:
		return err
	}
}

// FilenameFor builds a sortable, collision-resistant name from the timestamp
// and message body. Underscore separator keeps the timestamp's own dashes
// distinguishable from the hash prefix. Exported so adapters that point at the
// file (e.g. the pane-ping messenger) can derive the same name the inbox
// messenger would write for the same (t, message).
func FilenameFor(t time.Time, message string) string {
	sum := sha256.Sum256([]byte(message))
	hash := hex.EncodeToString(sum[:])[:8]
	return strconv.FormatInt(t.UnixNano(), 10) + "_" + hash + ".md"
}

// sendTimeKey scopes the shared "Send timestamp" composite injects so inbox
// and panep derive the same filename for one Send call. The key is unexported
// — only the inbox.WithTime / TimeFromContext helpers can read or write it.
type sendTimeKey struct{}

// WithTime attaches t to ctx as the shared timestamp the inbox messenger and
// any peers (panep) should use when deriving a filename via FilenameFor. The
// composite messenger calls this so a single Send produces one filename across
// every inner messenger, regardless of how long inbox's file I/O takes.
func WithTime(ctx context.Context, t time.Time) context.Context {
	return context.WithValue(ctx, sendTimeKey{}, t)
}

// TimeFromContext returns the timestamp WithTime stashed on ctx, falling back
// to fallback() if none is present. Callers running outside the composite
// (e.g. tests, direct use of inbox.Messenger) just get the fallback.
func TimeFromContext(ctx context.Context, fallback func() time.Time) time.Time {
	if t, ok := ctx.Value(sendTimeKey{}).(time.Time); ok {
		return t
	}
	return fallback()
}
