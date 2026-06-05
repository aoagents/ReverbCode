import { useState } from "react";
import { BOARD_COLUMNS, statusMeta, type ColumnKey, type Session } from "../lib/api";
import { Card } from "./Card";
import { Icon, I } from "./Icon";

export function Board(props: {
  sessions: Session[];
  demo: boolean;
  onToggleDemo: () => void;
  onMove: (id: string, col: ColumnKey) => void;
  onOpen: (id: string) => void;
  onCleanup: () => void;
}) {
  const [doneOpen, setDoneOpen] = useState(false);
  const [dragOver, setDragOver] = useState<ColumnKey | null>(null);

  const byCol: Record<ColumnKey, Session[]> = {
    working: [],
    needs: [],
    review: [],
    merge: [],
    done: [],
  };
  for (const s of props.sessions) byCol[statusMeta(s.status).column].push(s);

  return (
    <div className="board-wrap">
      <div className="board-header">
        <div>
          <h1>Board</h1>
          <p className="board-sub">
            Live agent sessions flowing from work → review → merge.
          </p>
        </div>
        <div style={{ display: "flex", gap: 8 }}>
          <button
            className={`btn-ghost sm ${props.demo ? "demo-on" : ""}`}
            onClick={props.onToggleDemo}
            title="Preview the board with sample cards"
          >
            {props.demo ? "Demo: on" : "Demo"}
          </button>
          <button className="btn-ghost sm" onClick={props.onCleanup}>
            Cleanup
          </button>
        </div>
      </div>

      <div className="board">
        {BOARD_COLUMNS.map((col) => {
          const items = byCol[col.key];
          return (
            <div className="col" key={col.key}>
              <div className="col-head">
                <span className={`cdot tone-${col.tone}`} />
                <span className="col-title">{col.title}</span>
                <span className="col-count">{items.length}</span>
              </div>
              <div
                className={`col-body ${dragOver === col.key ? "drag-over" : ""}`}
                onDragOver={(e) => {
                  e.preventDefault();
                  if (dragOver !== col.key) setDragOver(col.key);
                }}
                onDragLeave={(e) => {
                  if (!e.currentTarget.contains(e.relatedTarget as Node)) {
                    setDragOver((d) => (d === col.key ? null : d));
                  }
                }}
                onDrop={(e) => {
                  e.preventDefault();
                  const id = e.dataTransfer.getData("text/plain");
                  if (id) props.onMove(id, col.key);
                  setDragOver(null);
                }}
              >
                {items.map((s) => (
                  <Card key={s.id} session={s} onOpen={props.onOpen} />
                ))}
              </div>
            </div>
          );
        })}
      </div>

      <div className="done-bar">
        <button className="done-toggle" onClick={() => setDoneOpen((v) => !v)}>
          <span className={`chev ${doneOpen ? "open" : ""}`}>
            <Icon d={I.chevron} size={12} />
          </span>
          Done / Terminated
          <span className="col-count">{byCol.done.length}</span>
        </button>
        {doneOpen && (
          <div className="done-grid">
            {byCol.done.length === 0 ? (
              <span className="done-empty">Nothing here yet.</span>
            ) : (
              byCol.done.map((s) => (
                <Card key={s.id} session={s} onOpen={props.onOpen} />
              ))
            )}
          </div>
        )}
      </div>
    </div>
  );
}
