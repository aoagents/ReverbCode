package sse

import (
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
)

func TestEventsReplayAfterHeaderPrecedenceAndFiltering(t *testing.T) {
	src := &memorySource{events: []cdc.Event{
		testEvent(1, "ao", cdc.EventSessionCreated),
		testEvent(2, "ao", cdc.EventNotificationCreated),
		testEvent(3, "mer", cdc.EventNotificationCreated),
		testEvent(4, "ao", cdc.EventNotificationUpdated),
	}}
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: cdc.NewBroadcaster(), HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?after=1&projectId=ao&topics=notifications", nil)
	req.Header.Set("Last-Event-ID", "3")
	body := readStreamUntil(t, srv.Client(), req, "id: 4")
	if strings.Contains(body, "id: 1") || strings.Contains(body, "id: 3") || !strings.Contains(body, "id: 2") || !strings.Contains(body, "id: 4") {
		t.Fatalf("filtered replay body:\n%s", body)
	}
}

func TestEventsReplayFromLastEventID(t *testing.T) {
	src := &memorySource{events: []cdc.Event{
		testEvent(1, "ao", cdc.EventNotificationCreated),
		testEvent(2, "ao", cdc.EventNotificationUpdated),
	}}
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: cdc.NewBroadcaster(), HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?topics=notifications", nil)
	req.Header.Set("Last-Event-ID", "1")
	body := readStreamUntil(t, srv.Client(), req, "id: 2")
	if strings.Contains(body, "id: 1") || !strings.Contains(body, "id: 2") {
		t.Fatalf("Last-Event-ID replay body:\n%s", body)
	}
}

func TestEventsReplayBatches(t *testing.T) {
	src := &memorySource{events: []cdc.Event{
		testEvent(1, "ao", cdc.EventNotificationCreated),
		testEvent(2, "ao", cdc.EventNotificationUpdated),
		testEvent(3, "ao", cdc.EventNotificationUpdated),
	}}
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: cdc.NewBroadcaster(), ReplayBatchSize: 1, HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?after=0&topics=notifications", nil)
	body := readStreamUntil(t, srv.Client(), req, "id: 3")
	if !strings.Contains(body, "id: 1") || !strings.Contains(body, "id: 2") || !strings.Contains(body, "id: 3") {
		t.Fatalf("batched replay body:\n%s", body)
	}
}

func TestEventsFreshLiveOnlyStream(t *testing.T) {
	src := &memorySource{events: []cdc.Event{testEvent(1, "ao", cdc.EventNotificationCreated)}}
	bc := cdc.NewBroadcaster()
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: bc, HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?topics=notifications", nil)
	done := make(chan string, 1)
	go func() {
		done <- readStreamUntil(t, srv.Client(), req, "id: 2")
	}()
	time.Sleep(20 * time.Millisecond)
	bc.Publish(testEvent(2, "ao", cdc.EventNotificationUpdated))
	body := <-done
	if strings.Contains(body, "id: 1") || !strings.Contains(body, "id: 2") {
		t.Fatalf("fresh stream body:\n%s", body)
	}
}

func TestEventsNoDuplicateAcrossReplayLiveBoundary(t *testing.T) {
	bc := cdc.NewBroadcaster()
	src := &memorySource{events: []cdc.Event{
		testEvent(1, "ao", cdc.EventNotificationCreated),
		testEvent(2, "ao", cdc.EventNotificationUpdated),
	}}
	src.onRead = func() {
		bc.Publish(testEvent(2, "ao", cdc.EventNotificationUpdated))
		bc.Publish(testEvent(3, "ao", cdc.EventNotificationUpdated))
	}
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: bc, HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?after=0&topics=notifications", nil)
	body := readStreamUntil(t, srv.Client(), req, "id: 3")
	if strings.Count(body, "id: 2") != 1 || !strings.Contains(body, "id: 3") {
		t.Fatalf("dedupe body:\n%s", body)
	}
}

func TestEventsHeartbeatAndInvalidOffset(t *testing.T) {
	src := &memorySource{}
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: cdc.NewBroadcaster(), HeartbeatInterval: time.Millisecond})
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/events?after=wat")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("invalid offset status=%d body=%s", resp.StatusCode, body)
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?after=0", nil)
	body := readStreamUntil(t, srv.Client(), req, ": heartbeat")
	if !strings.Contains(body, ": heartbeat") {
		t.Fatalf("heartbeat body:\n%s", body)
	}
}

func TestEventsSlowClientGetsStreamError(t *testing.T) {
	bc := cdc.NewBroadcaster()
	release := make(chan struct{})
	src := &memorySource{}
	src.blockRead = release
	srv := httptest.NewServer(Handler{Source: src, Broadcaster: bc, LiveBufferSize: 1, HeartbeatInterval: time.Hour})
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/events?after=0&topics=notifications", nil)
	done := make(chan string, 1)
	go func() {
		done <- readStreamUntil(t, srv.Client(), req, "CLIENT_TOO_SLOW")
	}()
	time.Sleep(20 * time.Millisecond)
	bc.Publish(testEvent(1, "ao", cdc.EventNotificationCreated))
	bc.Publish(testEvent(2, "ao", cdc.EventNotificationUpdated))
	close(release)

	select {
	case body := <-done:
		if !strings.Contains(body, "event: stream_error") || !strings.Contains(body, "CLIENT_TOO_SLOW") {
			t.Fatalf("slow body:\n%s", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for slow-client stream_error")
	}
}

type memorySource struct {
	mu        sync.Mutex
	events    []cdc.Event
	onRead    func()
	blockRead <-chan struct{}
	readOnce  sync.Once
}

func (s *memorySource) EventsAfter(ctx context.Context, after int64, limit int) ([]cdc.Event, error) {
	s.readOnce.Do(func() {
		if s.onRead != nil {
			s.onRead()
		}
	})
	if s.blockRead != nil {
		select {
		case <-s.blockRead:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []cdc.Event
	for _, e := range s.events {
		if e.Seq > after {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (s *memorySource) LatestSeq(context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return 0, nil
	}
	return s.events[len(s.events)-1].Seq, nil
}

func testEvent(seq int64, project string, eventType cdc.EventType) cdc.Event {
	payload, _ := json.Marshal(map[string]any{"id": seq})
	return cdc.Event{
		Seq:       seq,
		ProjectID: project,
		SessionID: project + "-1",
		Type:      eventType,
		Payload:   payload,
		CreatedAt: time.Date(2026, 5, 31, 10, 30, int(seq), 0, time.UTC),
	}
}

func readStreamUntil(t *testing.T, client *http.Client, req *http.Request, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(req.Context(), 2*time.Second)
	defer cancel()
	req = req.WithContext(ctx)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var b strings.Builder
	buf := make([]byte, 256)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			b.Write(buf[:n])
			if strings.Contains(b.String(), want) {
				return b.String()
			}
		}
		if err != nil {
			t.Fatalf("read stream before %q: %v body=%s", want, err, b.String())
		}
	}
}
