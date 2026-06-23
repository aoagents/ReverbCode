// client.go - loopback TCP client helpers that mirror pty-client.ts.
// Each function dials the host addr fresh (short-lived connection) and
// returns without maintaining state. Cross-platform: uses only stdlib net.
package conpty

import (
	"encoding/json"
	"net"
	"time"
)

const (
	// ptyInputChunkRunes is the max runes per terminal-input frame.
	// Mirrors PTY_INPUT_CHUNK_CHARS in pty-client.ts.
	ptyInputChunkRunes = 512
	// ptyInputChunkDelay is the inter-chunk delay. Mirrors PTY_INPUT_CHUNK_DELAY_MS.
	ptyInputChunkDelay = 15 * time.Millisecond
	// ptyInputEnterDelay is the pause before sending Enter. Mirrors PTY_INPUT_ENTER_DELAY_MS.
	ptyInputEnterDelay = 300 * time.Millisecond

	dialTimeout      = 3 * time.Second
	getOutputTimeout = 3 * time.Second
	isAliveTimeout   = 2 * time.Second
)

// dialHost opens a TCP connection to addr with a deadline. Callers close it.
func dialHost(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, timeout)
}

// clientSendMessage chunks message by 512 runes and sends each as a
// MsgTerminalInput frame with 15ms gaps, then pauses 300ms and sends "\r".
// Mirrors ptyHostSendMessage from pty-client.ts.
func clientSendMessage(addr, message string) error {
	conn, err := dialHost(addr, dialTimeout)
	if err != nil {
		return err
	}
	defer conn.Close()

	runes := []rune(message)
	for i := 0; i < len(runes); i += ptyInputChunkRunes {
		end := i + ptyInputChunkRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])
		frame := EncodeMessage(MsgTerminalInput, []byte(chunk))
		if _, err := conn.Write(frame); err != nil {
			return err
		}
		// Inter-chunk delay only between chunks, not after the last one.
		if end < len(runes) {
			time.Sleep(ptyInputChunkDelay)
		}
	}

	// Brief pause before Enter (matches TS: Enter sent as a separate frame).
	time.Sleep(ptyInputEnterDelay)
	frame := EncodeMessage(MsgTerminalInput, []byte("\r"))
	_, err = conn.Write(frame)
	return err
}

// clientGetOutput sends MsgGetOutputReq and reads frames until MsgGetOutputRes.
// Returns "" on timeout or connection failure (no error), matching the TS.
// lines <= 0 is handled by the caller (runtime.go rejects it before calling).
func clientGetOutput(addr string, lines int) (string, error) {
	conn, err := dialHost(addr, getOutputTimeout)
	if err != nil {
		return "", nil // ponytail: connect failure -> "" like the TS
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(getOutputTimeout))

	req, _ := json.Marshal(GetOutputReq{Lines: lines})
	if _, err := conn.Write(EncodeMessage(MsgGetOutputReq, req)); err != nil {
		return "", nil
	}

	resultC := make(chan string, 1)
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		if msgType == MsgGetOutputRes {
			select {
			case resultC <- string(payload):
			default:
			}
		}
	})

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		select {
		case text := <-resultC:
			return text, nil
		default:
		}
		if err != nil {
			break
		}
	}
	// Drain the channel one last time after the read loop ends.
	select {
	case text := <-resultC:
		return text, nil
	default:
		return "", nil // timeout or EOF before response
	}
}

// clientIsAlive probes the host with MsgStatusReq. Returns true if a valid
// MsgStatusRes with parseable JSON is received. Connect failure or timeout
// returns false. Mirrors ptyHostIsAlive from pty-client.ts: host reachable
// == alive, regardless of the inner agent's alive field.
func clientIsAlive(addr string) bool {
	conn, err := dialHost(addr, isAliveTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(isAliveTimeout))

	if _, err := conn.Write(EncodeMessage(MsgStatusReq, nil)); err != nil {
		return false
	}

	aliveC := make(chan bool, 1)
	parser := NewMessageParser(func(msgType byte, payload []byte) {
		if msgType == MsgStatusRes {
			var sp StatusPayload
			alive := json.Unmarshal(payload, &sp) == nil
			select {
			case aliveC <- alive:
			default:
			}
		}
	})

	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			parser.Feed(buf[:n])
		}
		select {
		case result := <-aliveC:
			return result
		default:
		}
		if err != nil {
			break
		}
	}
	select {
	case result := <-aliveC:
		return result
	default:
		return false
	}
}

// clientKill sends MsgKillReq best-effort. Connect failure is a no-op
// (host already dead). Mirrors ptyHostKill from pty-client.ts.
func clientKill(addr string) error {
	conn, err := dialHost(addr, isAliveTimeout)
	if err != nil {
		return nil // already dead
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(isAliveTimeout))
	_, _ = conn.Write(EncodeMessage(MsgKillReq, nil))
	return nil
}
