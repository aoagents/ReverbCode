import React from "react";

const events = [
  ["sess_8f2", "ci.fail → nudge", "fail"],
  ["sess_a13", "pr.opened", "ok"],
  ["sess_b88", "merge.conflict → resolve", "warn"],
  ["sess_c01", "review.requested → reply", "review"],
  ["sess_d44", "check.pass", "ok"],
  ["sess_e72", "agent.spawn(codex)", "accent"],
  ["sess_f19", "worktree.adopt", "review"],
  ["sess_g05", "ci.fail → nudge", "fail"],
  ["sess_h33", "pr.merged", "ok"],
  ["sess_i48", "scm.diff(semantic)", "review"],
  ["sess_j61", "reaper.reclaim", "review"],
  ["sess_k77", "agent.spawn(claude-code)", "accent"],
];

export default function TickerBar() {
  const Items = (
    <>
      {events.map((e, i) => (
        <Item key={i} sess={e[0]} ev={e[1]} tone={e[2]} />
      ))}
    </>
  );
  return (
    <div
      data-testid="ticker-bar"
      className="relative bg-[color:var(--bg-deep)] text-[color:var(--fg-muted)] overflow-hidden border-b border-[color:var(--border)]"
    >
      <div className="ticker-track py-2">
        {Items}
        {Items}
      </div>
    </div>
  );
}

function Item({ sess, ev, tone }) {
  const color =
    tone === "fail"
      ? "text-[color:var(--status-fail)]"
      : tone === "warn"
      ? "text-[color:var(--status-warn)]"
      : tone === "ok"
      ? "text-[color:var(--status-ok)]"
      : tone === "accent"
      ? "text-[color:var(--accent)]"
      : "text-[color:var(--fg-muted)]";
  return (
    <div className="flex items-center gap-3 px-6 font-mono text-[11px] uppercase tracking-[0.18em] whitespace-nowrap">
      <span className="text-[color:var(--fg-dim)]">{sess}</span>
      <span className={color}>▸ {ev}</span>
      <span className="text-[color:var(--fg-dim)] opacity-50">·</span>
    </div>
  );
}
