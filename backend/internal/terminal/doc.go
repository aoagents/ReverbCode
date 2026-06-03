// Package terminal is the live-terminal streaming feature: it attaches to a
// session's Zellij pane over a PTY and multiplexes the byte stream to one or more
// WebSocket clients, alongside a session-state channel fed by the CDC
// broadcaster.
//
// Boundaries (see docs/architecture.md):
//
//   - This package owns the product workflow: PTY attach, output fan-out, a
//     bounded replay buffer, re-attach resilience, and the ch-tagged wire
//     protocol. It is transport-agnostic: it speaks to a small wsConn interface,
//     not to any concrete WebSocket library.
//   - internal/httpd owns the HTTP/WebSocket upgrade and adapts the accepted
//     socket to wsConn; it does not contain stream logic.
//   - The PTY itself is reached through PTYSource (satisfied by the Zellij runtime
//     adapter's AttachCommand/IsAlive) and spawned through an injectable
//     spawnFunc, so the fan-out, buffering, and re-attach logic test without a
//     real process, Zellij, or network.
//
// Raw PTY bytes never flow through the CDC change_log; only the session channel
// is fed by cdc.Broadcaster. Terminal output is high-volume ephemeral data and
// goes straight from the PTY to the socket.
package terminal
