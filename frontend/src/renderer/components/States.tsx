import type { Tab } from "./TopBar";

export function ComingSoon({ tab }: { tab: Tab }) {
  return (
    <div className="coming-soon">
      <div className="big">⊘</div>
      <h3>{tab[0].toUpperCase() + tab.slice(1)} — not wired yet</h3>
      <p>
        {tab === "reviews"
          ? "The PR review surface needs the daemon's PR endpoints, which are not implemented yet."
          : "An activity feed needs the daemon's event/SSE stream, which is not exposed yet."}
      </p>
    </div>
  );
}

export function DaemonDown() {
  return (
    <div className="daemon-down">
      <div style={{ fontSize: 40, opacity: 0.5 }}>⚠</div>
      <h3>Daemon not reachable</h3>
      <p>
        The app talks to the local <code>ao</code> daemon on <code>127.0.0.1</code>.
        Start it from the backend directory:
      </p>
      <pre>cd backend{"\n"}go run ./cmd/ao start</pre>
      <p style={{ fontSize: 11, color: "var(--fg-dim)" }}>
        This view reconnects automatically once the daemon is up.
      </p>
    </div>
  );
}
