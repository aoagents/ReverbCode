import { useRef } from "react";

/*
 * Terminal — SCAFFOLD ONLY.
 *
 * This is the slot a teammate fills in by porting emdash's terminal. The
 * surrounding shell (header, connection state, mount node, layout) is done;
 * the xterm rendering + wire protocol are intentionally NOT implemented here.
 *
 * What's already provided for the implementer:
 *   - `mountRef`            : attach the xterm instance to this element.
 *   - `props.sessionId`     : which session's PTY to attach to.
 *   - `props.connected`     : drive the header status dot from your socket.
 *
 * How to wire it (referencing emdash):
 *   1. The daemon exposes the PTY over a WebSocket at  ws://127.0.0.1:<port>/mux
 *      (loopback). The renderer is sandboxed, so open the socket in the Electron
 *      MAIN process and relay frames over IPC — mirror the existing `ao:request`
 *      bridge in src/main.ts + src/preload.ts (add an `ao:mux` channel).
 *   2. Frame protocol (see backend/internal/terminal/protocol.go), JSON text
 *      frames over the socket:
 *        client → server: { ch, id?, type, data?, cols?, rows? }
 *        server → client: { ch, id?, type, data?, error?, session? }
 *      Map keystrokes → {type:"data"}, resize → {type:"resize", cols, rows},
 *      and server {type:"data"} → term.write(frame.data).
 *   3. Use @xterm/xterm + @xterm/addon-fit (emdash's setup) for rendering/fit.
 */

export function Terminal(props: { sessionId: string; connected?: boolean }) {
  const mountRef = useRef<HTMLDivElement>(null);

  return (
    <div className="terminal">
      <div className="terminal-head">
        <span className={`term-dot ${props.connected ? "on" : ""}`} />
        <span className="term-title">terminal</span>
        <span className="term-session">{props.sessionId}</span>
      </div>
      {/* xterm mounts here (teammate / emdash port) */}
      <div ref={mountRef} className="terminal-body" data-session={props.sessionId}>
        <pre className="terminal-placeholder">
          <span className="tp-dim"># terminal scaffold — emdash xterm mounts here</span>
          {"\n"}
          <span className="tp-dim"># attaches to</span> /mux{" "}
          <span className="tp-dim">for session</span> {props.sessionId}
          {"\n\n"}
          <span className="tp-prompt">$</span> <span className="tp-cursor">▋</span>
        </pre>
      </div>
    </div>
  );
}
