package httpd

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

type fakeEventSource struct {
	live                    *fakeEventSubscriber
	sawSubscriptionOnReplay bool
}

func (s *fakeEventSource) EventsAfter(context.Context, int64, int) ([]cdc.Event, error) {
	s.sawSubscriptionOnReplay = s.live.hasSubscriber()
	s.live.publish(testCDCEvent(2))
	return []cdc.Event{testCDCEvent(1)}, nil
}

func (*fakeEventSource) LatestSeq(context.Context) (int64, error) {
	return 0, nil
}

type fakeEventSubscriber struct {
	mu sync.Mutex
	fn func(cdc.Event)
}

func (s *fakeEventSubscriber) Subscribe(fn func(cdc.Event)) func() {
	s.mu.Lock()
	s.fn = fn
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		s.fn = nil
		s.mu.Unlock()
	}
}

func (s *fakeEventSubscriber) hasSubscriber() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fn != nil
}

func (s *fakeEventSubscriber) publish(e cdc.Event) {
	s.mu.Lock()
	fn := s.fn
	s.mu.Unlock()
	if fn != nil {
		fn(e)
	}
}

func TestEventsStreamSubscribesBeforeReplayAndDrainsBufferedLive(t *testing.T) {
	live := &fakeEventSubscriber{}
	src := &fakeEventSource{live: live}
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{
		CDC:    src,
		Events: live,
	}, ControlDeps{})
	ts := httptest.NewServer(router)
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events?after=0", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/events: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	ids := readSSEIDs(t, resp.Body, 2)
	if got, want := strings.Join(ids, ","), "1,2"; got != want {
		t.Fatalf("ids = %s, want %s", got, want)
	}
	if !src.sawSubscriptionOnReplay {
		t.Fatal("replay started before live subscription was installed")
	}
}

func TestEventsStreamRejectsInvalidAfter(t *testing.T) {
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil, APIDeps{
		CDC:    &fakeEventSource{live: &fakeEventSubscriber{}},
		Events: &fakeEventSubscriber{},
	}, ControlDeps{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events?after=nope", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "INVALID_AFTER") {
		t.Fatalf("body = %s, want INVALID_AFTER", rec.Body.String())
	}
}

func readSSEIDs(t *testing.T, r io.Reader, want int) []string {
	t.Helper()
	ids := make([]string, 0, want)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "id: ") {
			ids = append(ids, strings.TrimPrefix(line, "id: "))
			if len(ids) == want {
				return ids
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	t.Fatalf("stream ended after ids %v, want %d ids", ids, want)
	return nil
}

func testCDCEvent(seq int64) cdc.Event {
	return cdc.Event{
		Seq:       seq,
		ProjectID: "proj_1",
		SessionID: "sess_1",
		Type:      cdc.EventSessionUpdated,
		Payload:   json.RawMessage(`{"status":"running"}`),
		CreatedAt: time.Unix(seq, 0).UTC(),
	}
}
