// Package terminaldiag writes an always-on terminal lifecycle trace used to
// debug renderer <-> mux <-> PTY attachment failures. It deliberately logs
// event metadata only; terminal bytes and typed input contents must never be
// recorded here.
package terminaldiag

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	defaultLogName = "terminal-flow.log"
	maxStringLen   = 500
)

var mu sync.Mutex

// Log appends one JSONL lifecycle event. It is best-effort: diagnostics must
// never affect terminal behavior.
func Log(side, event string, fields map[string]any) {
	if side == "" || event == "" {
		return
	}
	path, err := logPath()
	if err != nil {
		return
	}

	entry := map[string]any{
		"ts":     time.Now().Format(time.RFC3339Nano),
		"side":   side,
		"pid":    os.Getpid(),
		"event":  event,
		"fields": sanitizeFields(fields),
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	mu.Lock()
	defer mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = file.Write(line)
}

func logPath() (string, error) {
	if p := os.Getenv("AO_TERMINAL_FLOW_LOG"); p != "" {
		return p, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ao", defaultLogName), nil
}

func sanitizeFields(fields map[string]any) map[string]any {
	if len(fields) == 0 {
		return map[string]any{}
	}
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		if key == "" {
			continue
		}
		out[key] = sanitizeValue(value)
	}
	return out
}

func sanitizeValue(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return truncate(v)
	case bool:
		return v
	case int:
		return v
	case int64:
		return v
	case uint64:
		return v
	case uint16:
		return v
	case float64:
		return v
	case error:
		return truncate(v.Error())
	default:
		return truncate(toString(v))
	}
}

func truncate(s string) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", " ")
	if len(s) <= maxStringLen {
		return s
	}
	return s[:maxStringLen] + "..."
}

func toString(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "unserializable"
	}
	return string(data)
}
