import React from "react";
import { motion } from "framer-motion";

const ports = [
  { name: "Agent", impls: "claude-code · codex · cursor · +20", accent: true },
  { name: "Runtime", impls: "tmux · zellij · conpty" },
  { name: "Workspace", impls: "git worktree" },
  { name: "SCM", impls: "GitHub" },
  { name: "Tracker", impls: "GitHub (defined)", muted: true },
  { name: "Reviewer", impls: "claude-code" },
];

export default function Architecture() {
  return (
    <section
      id="architecture"
      data-testid="architecture-diagram"
      className="relative py-24 sm:py-32 border-t border-[color:var(--border)]"
    >
      <div className="container-page">
        <div className="grid lg:grid-cols-12 gap-8 mb-14 items-end">
          <div className="lg:col-span-7">
            <div className="serial-num text-xs font-mono mb-3">04 - architecture</div>
            <h2 className="font-display font-bold tracking-tight leading-[1.02] text-[color:var(--fg)]" style={{ fontSize: "clamp(32px, 4.8vw, 60px)" }}>
              A daemon at the center.{" "}
              <span className="font-editorial italic font-medium text-[color:var(--accent)]">Ports at the edges.</span>
            </h2>
          </div>
          <div className="lg:col-span-5">
            <p className="text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
              Hexagonal architecture. Inbound/outbound port contracts make every external
              system - agent, runtime, workspace, SCM - a swappable adapter.
            </p>
          </div>
        </div>

        <motion.div
          initial={{ opacity: 0, y: 16 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true }}
          transition={{ duration: 0.5 }}
          className="surface relative overflow-hidden"
        >
          <div className="relative px-6 sm:px-10 py-12 sm:py-16 dotgrid">
            {/* Clients */}
            <div className="flex flex-wrap justify-center gap-6 sm:gap-12 mb-8">
              <ClientNode label="ao CLI" sub="thin daemon client" />
              <ClientNode label="Electron app" sub="desktop supervisor" />
            </div>

            <Wires count={2} />

            {/* Daemon */}
            <div className="flex justify-center mb-10">
              <div className="relative">
                <div className="absolute -inset-px rounded-xl bg-gradient-to-br from-[color:var(--accent)] to-transparent opacity-40 blur-sm" />
                <div className="relative bg-[color:var(--bg-deep)] text-[color:var(--fg)] px-8 sm:px-14 py-6 sm:py-8 rounded-xl border border-[color:var(--accent)] glow-accent">
                  <div className="flex items-center gap-2 mb-2">
                    <span className="w-1.5 h-1.5 rounded-full bg-[color:var(--accent)] pulse-dot" />
                    <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--accent)]">
                      127.0.0.1 · loopback only
                    </span>
                  </div>
                  <div className="font-display font-bold text-3xl sm:text-4xl tracking-tight">
                    Go daemon
                  </div>
                  <div className="grid grid-cols-2 sm:grid-cols-4 gap-2 mt-4">
                    {["HTTP API", "Lifecycle mgr", "CDC stream", "SQLite store"].map((c) => (
                      <div
                        key={c}
                        className="border border-[color:var(--border-strong)] bg-[color:var(--bg-card)] px-2.5 py-1.5 rounded font-mono text-[10px] text-center uppercase tracking-wider text-[color:var(--fg-muted)]"
                      >
                        {c}
                      </div>
                    ))}
                  </div>
                </div>
              </div>
            </div>

            <Wires count={5} />

            {/* Ports */}
            <div className="grid grid-cols-2 sm:grid-cols-3 lg:grid-cols-6 gap-3">
              {ports.map((p) => (
                <Port key={p.name} port={p} />
              ))}
            </div>
          </div>

          {/* footer rail */}
          <div className="border-t border-[color:var(--border)] bg-[color:var(--bg-chrome)] px-6 sm:px-10 py-4 flex flex-wrap items-center justify-between gap-3 font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
            <span>
              <span className="text-[color:var(--fg-muted)]">ports/</span> defined in backend/internal/ports/
            </span>
            <span>
              <span className="text-[color:var(--fg-muted)]">adapters/</span> swappable · registered at boot
            </span>
            <span>
              <span className="text-[color:var(--fg-muted)]">events/</span> sse fan-out
            </span>
          </div>
        </motion.div>
      </div>
    </section>
  );
}

function ClientNode({ label, sub }) {
  return (
    <div className="text-center">
      <div className="surface-elev px-6 py-3 inline-block lift">
        <div className="font-display font-bold text-lg tracking-tight text-[color:var(--fg)]">
          {label}
        </div>
      </div>
      <div className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)] mt-2">
        {sub}
      </div>
    </div>
  );
}

function Port({ port }) {
  return (
    <div
      className={`surface px-3 py-3 lift ${
        port.muted ? "opacity-60" : ""
      } ${port.accent ? "border-[color:var(--accent)]" : ""}`}
    >
      <div className="flex items-center gap-1.5 mb-1">
        <span
          className="w-1 h-1 rounded-full"
          style={{ background: port.accent ? "var(--accent)" : "var(--fg-dim)" }}
        />
        <span className="font-mono text-[9px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
          port
        </span>
      </div>
      <div className="font-display font-bold text-[15px] tracking-tight text-[color:var(--fg)]">
        {port.name}
      </div>
      <div className="font-mono text-[10px] mt-1 text-[color:var(--fg-muted)] leading-snug">
        {port.impls}
      </div>
    </div>
  );
}

function Wires({ count }) {
  const paths = count === 2
    ? ["M260 0 L300 60", "M340 0 L300 60"]
    : ["M300 0 L80 60", "M300 0 L200 60", "M300 0 L300 60", "M300 0 L400 60", "M300 0 L520 60"];
  return (
    <div className="relative h-12 sm:h-16 flex justify-center mb-2">
      <svg viewBox="0 0 600 60" className="w-full max-w-[760px] h-full">
        {paths.map((d, i) => (
          <g key={i}>
            <path d={d} stroke="var(--wire)" strokeWidth="1.4" fill="none" />
            <path
              d={d}
              stroke="var(--accent)"
              strokeWidth="1.8"
              fill="none"
              strokeDasharray="16 200"
              style={{ animation: `wire-pulse 2.6s ease-in-out ${i * 0.3}s infinite` }}
            />
          </g>
        ))}
      </svg>
    </div>
  );
}
