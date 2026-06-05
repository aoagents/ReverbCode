import { statusMeta, type ProjectSummary, type Session } from "../lib/api";
import { shortId } from "../lib/format";
import { Icon, I } from "./Icon";

export function Sidebar(props: {
  projects: ProjectSummary[];
  sessions: Session[];
  selected: string | null;
  expanded: Set<string>;
  onSelectProject: (id: string) => void;
  onToggle: (id: string) => void;
  onOpenSession: (id: string) => void;
  onAddProject: () => void;
  onRemoveProject: (id: string) => void;
}) {
  return (
    <aside className="sidebar">
      <div className="sb-head">
        <span className="sb-label">Projects</span>
        <button className="icon-btn sm" onClick={props.onAddProject} title="Add project">
          <Icon d={I.plus} size={14} />
        </button>
      </div>

      <div className="sb-scroll">
        {props.projects.length === 0 ? (
          <div className="sb-empty">
            No projects.<br />Click + to add a repo.
          </div>
        ) : (
          props.projects.map((p) => {
            const sess = props.sessions.filter((s) => s.projectId === p.id);
            const open = props.expanded.has(p.id);
            return (
              <div key={p.id} className="sb-project-group">
                <div
                  className={`sb-project ${props.selected === p.id ? "active" : ""}`}
                  onClick={() => props.onSelectProject(p.id)}
                >
                  <button
                    className={`chev ${open ? "open" : ""}`}
                    onClick={(e) => {
                      e.stopPropagation();
                      props.onToggle(p.id);
                    }}
                  >
                    <Icon d={I.chevron} size={12} />
                  </button>
                  <span className="sb-project-name">{p.name}</span>
                  <span className="sb-count">{sess.length}</span>
                  <button
                    className="icon-btn sm danger sb-remove"
                    title="Remove project"
                    onClick={(e) => {
                      e.stopPropagation();
                      props.onRemoveProject(p.id);
                    }}
                  >
                    ✕
                  </button>
                </div>
                {open &&
                  sess.map((s) => {
                    const m = statusMeta(s.status);
                    return (
                      <div
                        key={s.id}
                        className="sb-session"
                        onClick={() => props.onOpenSession(s.id)}
                      >
                        <span className={`sdot tone-${m.tone}`} />
                        <span className="sb-session-name">
                          {s.displayName || s.id}
                        </span>
                        <span className="sb-session-id">{shortId(s.id)}</span>
                      </div>
                    );
                  })}
              </div>
            );
          })
        )}
      </div>

      <div className="sb-foot">
        <button className="icon-btn sm"><Icon d={I.home} /></button>
        <button className="icon-btn sm"><Icon d={I.check} /></button>
        <button className="icon-btn sm"><Icon d={I.swap} /></button>
        <button className="icon-btn sm"><Icon d={I.moon} /></button>
      </div>
    </aside>
  );
}
