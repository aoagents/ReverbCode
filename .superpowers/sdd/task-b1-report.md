# Task B1 Report: ConPTY Protocol Codec + Output Ring Buffer

## Status: DONE

Commit: `1050542`

## Files created

- `backend/internal/adapters/runtime/conpty/proto.go` -- constants, EncodeMessage, MessageParser, shared JSON structs
- `backend/internal/adapters/runtime/conpty/ring.go` -- Ring (Append, Snapshot, Tail, FlushPartial)
- `backend/internal/adapters/runtime/conpty/proto_test.go` -- 8 table-driven tests
- `backend/internal/adapters/runtime/conpty/ring_test.go` -- 7 table-driven tests

## Design notes

### proto.go

- `EncodeMessage` allocates `5+len(payload)` bytes, writes the type byte at [0],
  big-endian uint32 length at [1:5], copies payload at [5:]. Exact match of
  `encodeMessage` in pty-host.ts.
- `MessageParser.Feed` appends to an internal `[]byte` buffer and loops while
  `len(buf) >= 5`, reading the frame length, breaking if incomplete, then
  slicing type+payload, advancing the buffer past the frame, and calling
  onMessage. A `make+copy` is used for the payload (not a sub-slice) so callers
  that retain the slice are not corrupted on future buffer growth.
- JSON helper structs kept minimal per brief.

### ring.go

- `Append` prepends `partialLine`, splits on `"\n"`, stores all but the last
  element as `line+"\n"`, stores the last element as the new `partialLine`.
  Trims to `MaxOutputLines` after each Append. Exact match of `appendOutput`.
- `Snapshot` joins all stored lines (excludes `partialLine`), matches the
  TS `outputBuffer.join("")` used for scrollback.
- `Tail(n)` slices `lines[max(0,len-n):]` and joins, mirrors the
  MSG_GET_OUTPUT_REQ handler.
- `FlushPartial` pushes the partial line as a final entry (no `"\n"` appended),
  mirrors the `pty.onExit` handler.
- `sync.Mutex` guards all fields; Append and Snapshot/Tail are safe to call
  from separate goroutines.
- `ponytail:` comment marks the O(n) head-trim; a circular buffer would be the
  upgrade path if trim rate is high.

## Verification outputs

### go build ./...
```
(no output -- success)
```

### go test ./internal/adapters/runtime/conpty/... -v
```
15 tests PASS
--- PASS: TestEncodeMessage
--- PASS: TestEncodeMessageZeroPayload
--- PASS: TestParserSingleFrame
--- PASS: TestParserTwoFramesOneChunk
--- PASS: TestParserByteAtATime
--- PASS: TestParserInterleavedTypes
--- PASS: TestParserPayloadIsCopy
--- PASS: TestParserZeroLengthFrame
--- PASS: TestRingAppendPartialThenComplete
--- PASS: TestRingExceedsMaxOutputLines
--- PASS: TestRingFlushPartialNoNewline
--- PASS: TestRingTailEdgeCases
--- PASS: TestRingANSIRoundTrip
--- PASS: TestRingTailSubset
--- PASS: TestRingSnapshotExcludesPartial
ok  github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty
```

### go vet ./internal/adapters/runtime/conpty/...
```
(no output -- clean)
```

### GOOS=windows go build ./...
```
(no output -- success)
```

## Concerns

None. All four verification commands pass. No new dependencies added. No build
tags. No OS-specific calls. No em dashes anywhere in code, comments, or commit
message.

---

## Test-hardening follow-up (review approval, commit d67941b)

### Changes made (test files only)

**proto_test.go -- TestParserPayloadIsCopy (replaced)**

The prior test mutated the fed frame slice after Feed, which did not exercise
the real aliasing risk (Feed already copies the chunk into its internal buffer
before parsing). The replacement exercises the path that would actually regress:

1. Feed frame1 (payload "original"), capture its delivered `[]byte` pointer.
2. Feed frame2 with the same payload length ("XXXXXXXX") so the parser's
   internal buffer advances over the exact byte range frame1 occupied.
3. Assert frame1's captured bytes are still "original".

This catches a regression where payload was a raw `p.buf[5:frameLen]` subslice
instead of a `make+copy`.

**ring_test.go -- TestRingConcurrent (added)**

Spawns 10 writer goroutines (Append) and 10 reader goroutines (Snapshot + Tail),
each doing 100 iterations, all running concurrently on a shared Ring. They are
joined with a sync.WaitGroup. The test is a no-op without `-race`; under the
race detector any missing mutex coverage would produce a race report. No new
dependencies: `sync` was already imported by ring.go itself.

### Verification outputs (post-hardening)

#### go test -race ./internal/adapters/runtime/conpty/...
```
16 passed (TestRingConcurrent is the new 16th test)
```

#### go vet ./internal/adapters/runtime/conpty/...
```
(no output -- clean)
```
