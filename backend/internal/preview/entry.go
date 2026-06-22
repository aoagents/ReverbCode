package preview

import (
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var entryCandidates = []string{"index.html", "public/index.html", "dist/index.html", "build/index.html"}

// Entry is a workspace-local static frontend entrypoint.
type Entry struct {
	Path    string
	AbsPath string
	ModTime time.Time
	Size    int64
}

// DiscoverEntry returns the first supported HTML entrypoint that exists inside
// the workspace.
func DiscoverEntry(workspacePath string) (Entry, bool) {
	if strings.TrimSpace(workspacePath) == "" {
		return Entry{}, false
	}
	for _, candidate := range entryCandidates {
		file, ok := ConfinedPath(workspacePath, candidate)
		if !ok {
			continue
		}
		info, err := os.Stat(file)
		if err == nil && !info.IsDir() {
			return Entry{Path: candidate, AbsPath: file, ModTime: info.ModTime(), Size: info.Size()}, true
		}
	}
	return Entry{}, false
}

// ConfinedPath maps an asset path into workspacePath and rejects paths that
// escape the workspace root.
func ConfinedPath(workspacePath, assetPath string) (string, bool) {
	root, err := filepath.Abs(workspacePath)
	if err != nil || root == "" {
		return "", false
	}
	clean := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(assetPath)), "/")
	if clean == "" || clean == "." {
		clean = "index.html"
	}
	file := filepath.Join(root, filepath.FromSlash(clean))
	absFile, err := filepath.Abs(file)
	if err != nil {
		return "", false
	}
	rel, err := filepath.Rel(root, absFile)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return absFile, true
}

// FileURL builds the daemon preview/files URL for a workspace-local entry.
func FileURL(baseURL string, id domain.SessionID, entry string) string {
	u := normalizedBaseURL(baseURL)
	u.Path = "/api/v1/sessions/" + url.PathEscape(string(id)) + "/preview/files/" + escapePath(entry)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func normalizedBaseURL(raw string) url.URL {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		raw = "http://127.0.0.1:3001"
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return url.URL{Scheme: "http", Host: raw}
	}
	return *u
}

func escapePath(raw string) string {
	parts := strings.Split(raw, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}
