package terminal

// The wire protocol is a single multiplexed JSON stream tagged by channel
// ("ch"), mirroring the legacy Node mux server so the existing xterm client can
// connect unchanged. One socket carries every logical stream:
//
//	ch "terminal" — per-pane byte stream, keyed by an opaque runtime handle id
//	ch "subscribe" — the client opts into the session-state channel
//	ch "sessions"  — server-pushed session-state messages (CDC-fed)
//	ch "system"    — liveness; ws-level ping/pong also runs underneath
//
// Terminal payloads are base64 in the Data field: PTY output is arbitrary bytes
// and need not be valid UTF-8, which a raw JSON string could not carry.
const (
	chTerminal  = "terminal"
	chSubscribe = "subscribe"
	chSessions  = "sessions"
	chSystem    = "system"
)

// client message types (ch "terminal" unless noted).
const (
	msgOpen      = "open"
	msgData      = "data"
	msgResize    = "resize"
	msgClose     = "close"
	msgSubscribe = "subscribe" // ch "subscribe"
	msgPing      = "ping"      // ch "system"
)

// server message types.
const (
	msgOpened   = "opened"
	msgExited   = "exited"
	msgError    = "error"
	msgSnapshot = "snapshot" // ch "sessions"
	msgPong     = "pong"     // ch "system"
)

// clientMsg is one inbound frame. Fields are shared across channels; which are
// populated depends on Ch/Type.
type clientMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded keystrokes for ch "terminal" / type "data".
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// serverMsg is one outbound frame.
type serverMsg struct {
	Ch   string `json:"ch"`
	ID   string `json:"id,omitempty"`
	Type string `json:"type"`
	// Data is base64-encoded PTY output for ch "terminal" / type "data".
	Data    string         `json:"data,omitempty"`
	Error   string         `json:"error,omitempty"`
	Session *sessionUpdate `json:"session,omitempty"`
}

// sessionUpdate is the ch "sessions" payload: a single CDC change projected to
// the fields a client needs to refresh its view. It deliberately omits the raw
// change_log payload blob; the client refetches detail over the REST surface.
type sessionUpdate struct {
	Seq       int64  `json:"seq"`
	ProjectID string `json:"projectId"`
	SessionID string `json:"sessionId,omitempty"`
	EventType string `json:"eventType"`
}
