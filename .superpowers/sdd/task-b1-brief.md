# Task B1: conpty protocol codec + output ring buffer (cross-platform, pure Go)

## Goal
Create the OS-agnostic core of the Windows ConPTY runtime: the named-pipe binary
framing protocol (codec + streaming parser) and the bounded output ring buffer.
This is a faithful Go port of agent-orchestrator's TypeScript implementation. It
has NO OS-specific code and MUST be fully unit-tested and green on this Darwin
machine. No go-pty, no go-winio, no build tags in this task.

Repo: `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/ReverbCode`,
module in `backend/`, branch `migrate-zellij-to-tmux-conpty` checked out.
Module path prefix: `github.com/aoagents/agent-orchestrator/backend`.

Create the package: `internal/adapters/runtime/conpty` (this task adds only
`proto.go`, `ring.go`, and their tests; later tasks add the Windows pieces here).

## Reference (port faithfully — read these)
- `/Users/harshitsinghbhandari/Downloads/side-quests/rv-code/agent-orchestrator/packages/plugins/runtime-process/src/pty-host.ts`
  lines 33-92 (the MSG_* constants, `encodeMessage`, `MessageParser`) and lines
  138-178 (the rolling output buffer `appendOutput`, MAX_OUTPUT_LINES=1000).

## Part 1 — Protocol codec (`proto.go`)
Port the binary framing protocol. Frame layout: `[1-byte type][4-byte big-endian
length][payload]`.

Message type constants (exact values):
```go
const (
    MsgTerminalData  byte = 0x01 // host -> client: raw PTY output
    MsgTerminalInput byte = 0x02 // client -> host: raw keystrokes
    MsgResize        byte = 0x03 // client -> host: JSON {cols, rows}
    MsgGetOutputReq  byte = 0x04 // client -> host: JSON {lines}
    MsgGetOutputRes  byte = 0x05 // host -> client: UTF-8 text
    MsgStatusReq     byte = 0x06 // client -> host: empty
    MsgStatusRes     byte = 0x07 // host -> client: JSON {alive, pid, exitCode?}
    MsgKillReq       byte = 0x08 // client -> host: empty
)
```

Functions/types:
- `func EncodeMessage(msgType byte, payload []byte) []byte` — allocate `5 +
  len(payload)`, write type at [0], `binary.BigEndian.PutUint32` the length at
  [1:5], copy payload at [5:]. (A string convenience is not needed; callers pass
  `[]byte`.)
- `type MessageParser struct { ... }` with:
  - `func NewMessageParser(onMessage func(msgType byte, payload []byte)) *MessageParser`
  - `func (p *MessageParser) Feed(chunk []byte)` — append to an internal buffer,
    then loop: while buffered >= 5, read the BE uint32 length at [1:5], if the
    full frame (5+len) is not yet buffered break; otherwise slice type+payload,
    advance the buffer past the frame, and invoke onMessage. Port the exact
    semantics of the TS `MessageParser.feed` (it handles arbitrary chunk
    boundaries and multiple frames per chunk).
  - IMPORTANT: hand `onMessage` a COPY of the payload slice (not a sub-slice of
    the internal growable buffer), so a caller that retains the payload is not
    corrupted when the buffer is later reused/reallocated. (The TS version slices
    Buffers which are copy-on-concat; in Go you must copy explicitly.)

Add a couple of JSON helper structs the later tasks will share (keep minimal):
```go
type ResizePayload struct { Cols int `json:"cols"`; Rows int `json:"rows"` }
type StatusPayload struct { Alive bool `json:"alive"`; PID int `json:"pid"`; ExitCode *int `json:"exitCode,omitempty"` }
type GetOutputReq struct { Lines int `json:"lines"` }
```

## Part 2 — Output ring buffer (`ring.go`)
Port the rolling line buffer that preserves raw bytes (ANSI codes intact) for
xterm replay, capped at `MaxOutputLines = 1000`.

```go
type Ring struct { ... } // guarded by a sync.Mutex; safe for concurrent Append + snapshot
func NewRing() *Ring
func (r *Ring) Append(raw []byte)        // port appendOutput: maintain a partialLine, split on '\n', push completed "line\n" entries, trim to last MaxOutputLines
func (r *Ring) Snapshot() []byte          // join all buffered lines (for replay to a newly connected client — the full scrollback)
func (r *Ring) Tail(lines int) string     // last N lines joined (for GetOutput; mirror the host MSG_GET_OUTPUT_REQ handler: start = max(0, len-lines))
func (r *Ring) FlushPartial()             // on PTY exit, push any trailing partialLine (no newline) as a final entry
```
Semantics to match the TS `appendOutput` exactly:
- Maintain `partialLine string`. On Append: `text := partialLine + string(raw)`,
  split on "\n"; the last split element is the new `partialLine` (either "" if
  text ended in \n, or a partial); each earlier element is stored WITH its
  trailing "\n" re-appended (`line + "\n"`). Trim oldest entries so stored line
  count <= MaxOutputLines.
- `Snapshot` returns `[]byte` of all stored lines concatenated (this is what the
  host sends a new client as scrollback). Do NOT include the in-progress
  partialLine in Snapshot (matches TS, which only joins outputBuffer).
- `Tail(n)` joins the last n stored lines.

## Tests (REQUIRED — the runnable check, all run on Darwin)
`proto_test.go`:
- EncodeMessage produces correct bytes for a known type+payload (assert the 5-byte
  header and payload).
- MessageParser round-trips: feed a single full frame; feed two frames in one
  chunk; feed a frame split across multiple Feed calls (1 byte at a time) and
  confirm exactly one onMessage with correct type+payload; feed interleaved
  frames of different types. Confirm payload is a copy (mutate the fed slice after
  Feed and assert the delivered payload is unchanged).
- A zero-length payload frame (e.g. MsgStatusReq) parses correctly.
`ring_test.go`:
- Append partial then complete lines; Snapshot/Tail reflect the TS semantics.
- Exceeding MaxOutputLines trims the oldest.
- FlushPartial pushes a trailing no-newline line.
- Tail(n) with n greater than stored count returns all; n<=0 returns "".
- Raw bytes incl. ANSI escape sequences survive round-trip (store `\x1b[31mhi\x1b[0m\n`).

## Definition of done (run from `backend/`)
- `go build ./...` succeeds.
- `go test ./internal/adapters/runtime/conpty/...` passes.
- `go vet ./internal/adapters/runtime/conpty/...` is clean.
- `GOOS=windows go build ./...` still succeeds (this task adds no Windows code, so
  it must not break the Windows build either).

## Hard rules
- Pure Go, no new dependencies, no build tags, no OS-specific calls in this task.
- Never use em dashes ("—") in code, comments, or commit messages.
- Minimal and idiomatic (ponytail); match the surrounding repo style. Mark any
  deliberate shortcut with a `ponytail:` comment.
- Touch only files under `backend/internal/adapters/runtime/conpty/`.
- Commit on the current branch; message ends with the
  `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>` trailer (no em dashes).
