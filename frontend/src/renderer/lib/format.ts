// Small pure formatting helpers shared across components.

/** Compact a session id, e.g. "agent-orchestrator-2" → "ao-2". */
export function shortId(id: string): string {
  const parts = id.split("-");
  if (parts.length < 2) return id;
  const num = parts[parts.length - 1];
  const prefix = parts
    .slice(0, -1)
    .map((p) => p[0])
    .join("");
  return `${prefix}-${num}`;
}

export function humanizeId(id: string): string {
  return id.replace(/[-_]/g, " ");
}

export function fmtTime(iso: string): string {
  if (!iso) return "—";
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleString();
}

export function relTime(iso: string): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const sec = Math.floor((Date.now() - d.getTime()) / 1000);
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `${hr}h ago`;
  return `${Math.floor(hr / 24)}d ago`;
}
