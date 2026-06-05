import { Icon, I } from "./Icon";

export type Tab = "board" | "reviews" | "activity";

export function TopBar(props: {
  tab: Tab;
  onTab: (t: Tab) => void;
  projectName: string;
  up: boolean;
  workingCount: number;
  onNewAgent: () => void;
  canNewAgent: boolean;
}) {
  const tabs: Tab[] = ["board", "reviews", "activity"];
  return (
    <header className="topbar">
      <div className="tb-brand">
        <span className="brand-text">
          <span className="brand-dim">Reverb</span>Code
        </span>
      </div>

      <div className="tb-nav">
        <span className="tb-project">{props.projectName}</span>
        <nav className="tabs">
          {tabs.map((t) => (
            <button
              key={t}
              className={`tab ${props.tab === t ? "active" : ""}`}
              onClick={() => props.onTab(t)}
            >
              {t[0].toUpperCase() + t.slice(1)}
            </button>
          ))}
        </nav>
      </div>

      <div className="tb-right">
        <span className={`working-pill ${props.up ? "" : "down"}`}>
          <span className="wp-dot" />
          {props.up ? `${props.workingCount} working` : "offline"}
        </span>
        <button className="icon-btn" title="Notifications">
          <Icon d={I.bell} size={16} />
        </button>
        <button
          className="btn-primary"
          onClick={props.onNewAgent}
          disabled={!props.canNewAgent}
          title={props.canNewAgent ? "Spawn an agent" : "Add a project first"}
        >
          New agent
        </button>
      </div>
    </header>
  );
}
