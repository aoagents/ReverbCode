import { statusMeta, type Session } from "../lib/api";
import { humanizeId, relTime, shortId } from "../lib/format";
import { Icon, I } from "./Icon";

export function Card(props: { session: Session; onOpen: (id: string) => void }) {
  const s = props.session;
  const m = statusMeta(s.status);
  const title = s.displayName || humanizeId(s.id);
  return (
    <div
      className="card"
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData("text/plain", s.id);
        e.dataTransfer.effectAllowed = "move";
      }}
      onClick={() => props.onOpen(s.id)}
    >
      <div className="card-status">
        <span className={`sdot tone-${m.tone}`} />
        <span className="card-status-label">{m.label}</span>
        <span className="card-id">{shortId(s.id)}</span>
      </div>
      <div className="card-title">{title}</div>
      <div className="card-branch">
        <Icon d={I.branch} size={12} />
        <span>{s.harness ?? s.kind}</span>
      </div>
      <div className="card-foot">
        <span className="foot-meta">
          {s.kind === "orchestrator" ? "orchestrator" : s.activity?.state ?? "—"}
        </span>
        <span className="foot-time">{relTime(s.updatedAt)}</span>
      </div>
    </div>
  );
}
