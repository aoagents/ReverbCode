import { useCallback, useEffect, useRef, useState } from "react";
import {
  cleanupSessions,
  getHealth,
  listProjects,
  listSessions,
  removeProject,
  type ColumnKey,
  type Health,
  type ProjectSummary,
  type Session,
} from "./lib/api";
import { COLUMN_DEFAULT_STATUS, DEMO_SESSIONS } from "./lib/demo";
import { TopBar, type Tab } from "./components/TopBar";
import { Sidebar } from "./components/Sidebar";
import { Board } from "./components/Board";
import { ComingSoon, DaemonDown } from "./components/States";
import { SessionView } from "./components/SessionView";
import { AddProjectModal } from "./components/modals/AddProjectModal";
import { SpawnSessionModal } from "./components/modals/SpawnSessionModal";

const POLL_MS = 2000;

export function App() {
  const [health, setHealth] = useState<Health | null>(null);
  const [projects, setProjects] = useState<ProjectSummary[]>([]);
  const [sessions, setSessions] = useState<Session[]>([]);
  const [selected, setSelected] = useState<string | null>(null);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [tab, setTab] = useState<Tab>("board");
  const [detailId, setDetailId] = useState<string | null>(null);
  const [toast, setToast] = useState<string | null>(null);
  const [showAddProject, setShowAddProject] = useState(false);
  const [showSpawn, setShowSpawn] = useState(false);
  const [demo, setDemo] = useState(false);
  const [statusOverride, setStatusOverride] = useState<
    Record<string, Session["status"]>
  >({});

  const didAutoSelect = useRef(false);

  const flash = useCallback((msg: string) => {
    setToast(msg);
    window.setTimeout(() => setToast(null), 4000);
  }, []);

  const poll = useCallback(async () => {
    const h = await getHealth();
    setHealth(h);
    if (!h) {
      setProjects([]);
      setSessions([]);
      return;
    }
    try {
      const [ps, ss] = await Promise.all([listProjects(), listSessions()]);
      setProjects(ps);
      setSessions(ss);
    } catch (err) {
      flash(err instanceof Error ? err.message : String(err));
    }
  }, [flash]);

  useEffect(() => {
    void poll();
    const id = window.setInterval(() => void poll(), POLL_MS);
    return () => window.clearInterval(id);
  }, [poll]);

  // Auto-select + expand the first project once so the board isn't empty on load.
  useEffect(() => {
    if (didAutoSelect.current || projects.length === 0) return;
    didAutoSelect.current = true;
    setSelected(projects[0].id);
    setExpanded(new Set([projects[0].id]));
  }, [projects]);

  const up = !!health;
  const withOverride = (list: Session[]): Session[] =>
    list.map((s) =>
      statusOverride[s.id] ? { ...s, status: statusOverride[s.id] } : s,
    );
  const activeSessions = withOverride(demo ? DEMO_SESSIONS : sessions);
  const workingCount = activeSessions.filter((s) => s.status === "working").length;
  const visible = demo
    ? activeSessions
    : selected
      ? activeSessions.filter((s) => s.projectId === selected)
      : activeSessions;

  const moveSession = (id: string, col: ColumnKey) =>
    setStatusOverride((prev) => ({ ...prev, [id]: COLUMN_DEFAULT_STATUS[col] }));
  const selectedProject = projects.find((p) => p.id === selected) ?? null;

  const toggleExpand = (id: string) =>
    setExpanded((prev) => {
      const next = new Set(prev);
      next.has(id) ? next.delete(id) : next.add(id);
      return next;
    });

  return (
    <div className="app">
      <TopBar
        tab={tab}
        onTab={setTab}
        projectName={selectedProject?.name ?? "All projects"}
        up={up}
        workingCount={workingCount}
        onNewAgent={() => setShowSpawn(true)}
        canNewAgent={up && projects.length > 0}
      />

      <div className="body">
        <Sidebar
          projects={projects}
          sessions={sessions}
          selected={selected}
          expanded={expanded}
          onSelectProject={(id) => {
            setSelected(id);
            setExpanded((prev) => new Set(prev).add(id));
          }}
          onToggle={toggleExpand}
          onOpenSession={(id) => setDetailId(id)}
          onAddProject={() => setShowAddProject(true)}
          onRemoveProject={async (id) => {
            try {
              await removeProject(id);
              if (selected === id) setSelected(null);
              await poll();
            } catch (err) {
              flash(err instanceof Error ? err.message : String(err));
            }
          }}
        />

        <main className="main">
          {!up ? (
            <DaemonDown />
          ) : tab === "board" ? (
            <Board
              sessions={visible}
              demo={demo}
              onToggleDemo={() => setDemo((v) => !v)}
              onMove={moveSession}
              onOpen={(id) => setDetailId(id)}
              onCleanup={async () => {
                try {
                  const cleaned = await cleanupSessions(selected ?? undefined);
                  flash(`cleaned ${cleaned.length} session(s)`);
                  await poll();
                } catch (err) {
                  flash(err instanceof Error ? err.message : String(err));
                }
              }}
            />
          ) : (
            <ComingSoon tab={tab} />
          )}
        </main>
      </div>

      {showAddProject && (
        <AddProjectModal
          onClose={() => setShowAddProject(false)}
          onDone={async () => {
            setShowAddProject(false);
            await poll();
          }}
        />
      )}
      {showSpawn && (
        <SpawnSessionModal
          projects={projects}
          defaultProject={selected}
          onClose={() => setShowSpawn(false)}
          onDone={async () => {
            setShowSpawn(false);
            await poll();
          }}
        />
      )}
      {detailId && (
        <SessionView
          sessionId={detailId}
          fallback={sessions.find((s) => s.id === detailId) ?? null}
          projects={projects}
          onClose={() => setDetailId(null)}
          onChanged={poll}
          onError={flash}
        />
      )}
      {toast && <div className="toast">{toast}</div>}
    </div>
  );
}
