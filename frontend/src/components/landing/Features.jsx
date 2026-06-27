import React from "react";
import { motion } from "framer-motion";
import { Layers, GitBranch, Eye, Lock, Activity, Workflow, TerminalSquare } from "lucide-react";

const features = [
  {
    icon: Layers,
    kicker: "the substrate",
    title: "An operating system, not a wrapper.",
    desc: "Inbound and outbound port contracts. Swappable adapters. A CDC stream. The kind of substrate that survives the next model upgrade - and the one after that.",
    visual: "ports",
    accent: true,
    span: "lg:col-span-2",
  },
  {
    icon: GitBranch,
    kicker: "isolation",
    title: "Every agent gets a worktree.",
    desc: "No branch collisions. No stash gymnastics. Each session lives in its own git worktree with its own attachable pane.",
    visual: "branch",
    span: "lg:col-span-1",
  },
  {
    icon: Eye,
    kicker: "feedback loop",
    title: "PRs watched. Agents nudged.",
    desc: "CI failure, requested change, merge conflict - the lifecycle manager routes each fact back to the owning agent automatically.",
    visual: "pr",
    span: "lg:col-span-1",
  },
  {
    icon: Activity,
    kicker: "durability",
    title: "Durable facts. Derived status.",
    desc: "SQLite stores a small set of session facts. Display state is computed at read time. Triggers append to change_log; CDC fans events out via SSE.",
    visual: "log",
    span: "lg:col-span-2",
  },
  {
    icon: Lock,
    kicker: "trust model",
    title: "Bound to 127.0.0.1.",
    desc: "No auth, no CORS, no TLS. No SaaS in the loop. Your threat model fits on a sticky note.",
    span: "lg:col-span-1",
  },
  {
    icon: Workflow,
    kicker: "lifecycle",
    title: "Lifecycle manager + reaper.",
    desc: "Reduces runtime, activity and PR observations into durable state. Crash-safe reconcile on every boot.",
    span: "lg:col-span-1",
  },
  {
    icon: TerminalSquare,
    kicker: "interfaces",
    title: "ao CLI and Electron app.",
    desc: "Both drive the same daemon over loopback. Spawn from a terminal; supervise in a desktop kanban.",
    span: "lg:col-span-1",
  },
];

export default function Features() {
  return (
    <section id="features" data-testid="features-grid" className="relative py-24 sm:py-32">
      <div className="container-page">
        <div className="grid lg:grid-cols-12 gap-8 mb-14 items-end">
          <div className="lg:col-span-7">
            <div className="serial-num text-xs font-mono mb-3">02 - what&apos;s inside</div>
            <h2 className="font-display font-bold tracking-tight leading-[1.05] text-[color:var(--fg)]" style={{ fontSize: "clamp(32px, 4.5vw, 56px)" }}>
              Built like an operating system,{" "}
              <span className="font-editorial italic font-medium text-[color:var(--fg-muted)]">
                not a wrapper.
              </span>
            </h2>
          </div>
          <div className="lg:col-span-5">
            <p className="text-[15px] leading-relaxed text-[color:var(--fg-muted)]">
              Inbound/outbound port contracts. Swappable adapters. A CDC stream - the kind of
              substrate that survives the next model upgrade. And the one after that.
            </p>
          </div>
        </div>

        <div className="grid lg:grid-cols-3 gap-4">
          {features.map((f, i) => (
            <Card key={f.title} f={f} i={i} />
          ))}
        </div>
      </div>
    </section>
  );
}

function Card({ f, i }) {
  const Icon = f.icon;
  return (
    <motion.div
      initial={{ opacity: 0, y: 16 }}
      whileInView={{ opacity: 1, y: 0 }}
      viewport={{ once: true, margin: "-50px" }}
      transition={{ duration: 0.45, delay: i * 0.04 }}
      data-testid={`feature-article-${String(i + 1).padStart(2, "0")}`}
      className={`group surface lift p-6 relative overflow-hidden ${f.span || ""} ${
        f.accent ? "bg-gradient-to-br from-[color:var(--bg-card)] to-[#0d1220]" : ""
      }`}
    >
      <div className="flex items-center gap-2 mb-5">
        <div
          className={`w-8 h-8 rounded-md flex items-center justify-center border ${
            f.accent
              ? "bg-[color:var(--accent-soft)] border-[color:var(--accent)] text-[color:var(--accent)]"
              : "bg-[color:var(--bg-deep)] border-[color:var(--border-strong)] text-[color:var(--fg-muted)]"
          }`}
        >
          <Icon className="w-4 h-4" />
        </div>
        <span className="font-mono text-[10px] uppercase tracking-[0.22em] text-[color:var(--fg-dim)]">
          {f.kicker}
        </span>
      </div>
      <h3 className="font-display font-bold text-[19px] sm:text-[21px] tracking-tight leading-snug text-[color:var(--fg)] mb-2.5">
        {f.title}
      </h3>
      <p className="text-[14px] leading-relaxed text-[color:var(--fg-muted)]">
        {f.desc}
      </p>

      {f.visual === "ports" && <PortsVisual />}
      {f.visual === "branch" && <BranchVisual />}
      {f.visual === "pr" && <PRVisual />}
      {f.visual === "log" && <LogVisual />}
    </motion.div>
  );
}

function PortsVisual() {
  return (
    <div className="mt-6 pt-5 border-t border-[color:var(--border)] grid grid-cols-3 gap-2">
      {["Agent", "Runtime", "Workspace", "SCM", "Tracker", "Reviewer"].map((p, i) => (
        <div
          key={p}
          className="font-mono text-[10px] uppercase tracking-wider text-[color:var(--fg-muted)] flex items-center gap-1.5 py-1"
        >
          <span
            className="w-1 h-1 rounded-full"
            style={{ background: i === 0 ? "var(--accent)" : "var(--fg-dim)" }}
          />
          {p}
        </div>
      ))}
    </div>
  );
}

function BranchVisual() {
  return (
    <svg viewBox="0 0 200 60" className="mt-5 w-full h-auto opacity-90">
      <path d="M10 30 L60 30" stroke="rgba(255,255,255,0.25)" strokeWidth="1.5" fill="none" />
      <path d="M60 30 L60 10 L190 10" stroke="var(--accent)" strokeWidth="1.5" fill="none" />
      <path d="M60 30 L190 30" stroke="rgba(255,255,255,0.25)" strokeWidth="1.5" fill="none" />
      <path d="M60 30 L60 50 L190 50" stroke="var(--accent)" strokeWidth="1.5" fill="none" />
      <circle cx="10" cy="30" r="3" fill="var(--fg-muted)" />
      <circle cx="60" cy="30" r="3" fill="var(--fg)" />
      <circle cx="190" cy="10" r="3.5" fill="var(--accent)" />
      <circle cx="190" cy="30" r="3.5" fill="var(--fg-muted)" />
      <circle cx="190" cy="50" r="3.5" fill="var(--accent)" />
    </svg>
  );
}

function PRVisual() {
  return (
    <div className="mt-5 font-mono text-[11px] space-y-1">
      <Row dot="ok" label="lint" status="pass" />
      <Row dot="ok" label="unit" status="pass" />
      <Row dot="fail" label="e2e" status="fail" hl />
      <Row dot="review" label="review" status="requested" />
      <div className="mt-2 pt-2 border-t border-[color:var(--border)] text-[10px] uppercase tracking-wider text-[color:var(--accent)]">
        ↳ nudge → sess_8f2
      </div>
    </div>
  );
}

function LogVisual() {
  return (
    <div className="mt-5 font-mono text-[11px] space-y-0.5">
      {[
        ["0x1f", "sess.spawn", null],
        ["0x20", "pr.opened", null],
        ["0x21", "ci.fail → nudge", "fail"],
        ["0x22", "agent.resume", null],
        ["0x23", "pr.merged", "ok"],
      ].map((r) => (
        <div key={r[0]} className="flex justify-between">
          <span
            style={{
              color:
                r[2] === "fail"
                  ? "var(--status-fail)"
                  : r[2] === "ok"
                  ? "var(--status-ok)"
                  : "var(--fg-muted)",
            }}
          >
            {r[1]}
          </span>
          <span className="text-[color:var(--fg-dim)]">{r[0]}</span>
        </div>
      ))}
    </div>
  );
}

function Row({ dot, label, status, hl }) {
  const color =
    dot === "ok"
      ? "var(--status-ok)"
      : dot === "fail"
      ? "var(--status-fail)"
      : "var(--fg-muted)";
  return (
    <div className="flex items-center justify-between">
      <span className="flex items-center gap-1.5 text-[color:var(--fg)]">
        <span className="w-1.5 h-1.5 rounded-full" style={{ background: color }} />
        {label}
      </span>
      <span
        className="text-[10px] uppercase tracking-wider"
        style={{ color: hl ? "var(--status-fail)" : "var(--fg-muted)" }}
      >
        {status}
      </span>
    </div>
  );
}
