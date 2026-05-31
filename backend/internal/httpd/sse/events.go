package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

const (
	defaultReplayBatchSize = 512
	defaultLiveBufferSize  = 256
	defaultHeartbeat       = 15 * time.Second
)

type Handler struct {
	Source            cdc.Source
	Broadcaster       *cdc.Broadcaster
	ReplayBatchSize   int
	LiveBufferSize    int
	HeartbeatInterval time.Duration
}

func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.Source == nil {
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "EVENTS_UNAVAILABLE", "Event stream is not available", nil)
		return
	}
	filter, err := parseFilter(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_EVENT_STREAM_FILTER", err.Error(), nil)
		return
	}
	explicitAfter, hasExplicitAfter, err := explicitOffset(r)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_EVENT_STREAM_OFFSET", err.Error(), nil)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "EVENT_STREAM_UNSUPPORTED", "Response writer does not support streaming", nil)
		return
	}

	batch := h.ReplayBatchSize
	if batch <= 0 {
		batch = defaultReplayBatchSize
	}
	bufferSize := h.LiveBufferSize
	if bufferSize <= 0 {
		bufferSize = defaultLiveBufferSize
	}
	heartbeat := h.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = defaultHeartbeat
	}

	live := make(chan cdc.Event, bufferSize)
	slow := make(chan struct{})
	var slowOnce sync.Once
	if h.Broadcaster != nil {
		unsubscribe := h.Broadcaster.Subscribe(func(e cdc.Event) {
			if !filter.matches(e) {
				return
			}
			select {
			case live <- e:
			default:
				slowOnce.Do(func() { close(slow) })
			}
		})
		defer unsubscribe()
	}

	after := explicitAfter
	if !hasExplicitAfter {
		after, err = h.Source.LatestSeq(r.Context())
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "EVENT_STREAM_HEAD_FAILED", "Failed to determine event stream offset", nil)
			return
		}
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	_, _ = fmt.Fprint(w, "retry: 2000\n\n")
	flusher.Flush()

	lastSent := after
	cursor := after
	for {
		rows, err := h.Source.EventsAfter(r.Context(), cursor, batch)
		if err != nil {
			writeStreamError(w, flusher, "REPLAY_FAILED", "Failed to replay event stream")
			return
		}
		if len(rows) == 0 {
			break
		}
		cursor = rows[len(rows)-1].Seq
		for _, event := range rows {
			if !filter.matches(event) || event.Seq <= lastSent {
				continue
			}
			if err := writeEvent(w, flusher, event); err != nil {
				return
			}
			lastSent = event.Seq
		}
		if len(rows) < batch {
			break
		}
	}

drain:
	for {
		select {
		case event := <-live:
			if event.Seq > lastSent {
				if err := writeEvent(w, flusher, event); err != nil {
					return
				}
				lastSent = event.Seq
			}
		default:
			break drain
		}
	}

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-slow:
			writeStreamError(w, flusher, "CLIENT_TOO_SLOW", "SSE client could not keep up")
			return
		case event := <-live:
			if event.Seq <= lastSent {
				continue
			}
			if err := writeEvent(w, flusher, event); err != nil {
				return
			}
			lastSent = event.Seq
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		}
	}
}

func explicitOffset(r *http.Request) (int64, bool, error) {
	if raw := r.URL.Query().Get("after"); raw != "" {
		seq, err := parseOffset(raw, "after")
		return seq, true, err
	}
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		seq, err := parseOffset(raw, "Last-Event-ID")
		return seq, true, err
	}
	return 0, false, nil
}

func parseOffset(raw, name string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	return n, nil
}

type streamFilter struct {
	projectID string
	topics    map[string]struct{}
}

func parseFilter(r *http.Request) (streamFilter, error) {
	filter := streamFilter{projectID: r.URL.Query().Get("projectId")}
	rawTopics := strings.TrimSpace(r.URL.Query().Get("topics"))
	if rawTopics == "" {
		return filter, nil
	}
	filter.topics = map[string]struct{}{}
	for _, part := range strings.Split(rawTopics, ",") {
		topic := strings.TrimSpace(part)
		switch topic {
		case "sessions", "prs", "notifications":
			filter.topics[topic] = struct{}{}
		case "":
			continue
		default:
			return streamFilter{}, fmt.Errorf("topics contains unsupported topic %q", topic)
		}
	}
	return filter, nil
}

func (f streamFilter) matches(event cdc.Event) bool {
	if f.projectID != "" && event.ProjectID != f.projectID {
		return false
	}
	if len(f.topics) == 0 {
		return true
	}
	_, ok := f.topics[eventTopic(event)]
	return ok
}

func eventTopic(event cdc.Event) string {
	t := string(event.Type)
	switch {
	case strings.HasPrefix(t, "session_"):
		return "sessions"
	case strings.HasPrefix(t, "pr_"):
		return "prs"
	case strings.HasPrefix(t, "notification_"):
		return "notifications"
	default:
		return ""
	}
}

func writeEvent(w http.ResponseWriter, flusher http.Flusher, event cdc.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		writeStreamError(w, flusher, "ENCODE_FAILED", "Failed to encode event")
		return err
	}
	if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Seq, event.Type, data); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeStreamError(w http.ResponseWriter, flusher http.Flusher, code, message string) {
	data, _ := json.Marshal(map[string]string{"code": code, "message": message})
	_, _ = fmt.Fprintf(w, "event: stream_error\ndata: %s\n\n", data)
	flusher.Flush()
}
