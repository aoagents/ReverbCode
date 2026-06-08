import { useCallback, useEffect, useState } from "react";
import {
  getSession,
  killSession,
  renameSession,
  restoreSession,
  sendSessionMessage,
  statusMeta,
  type ProjectSummary,
  type Session,
} from "../lib/api";
import { fmtTime } from "../lib/format";
import { Terminal } from "./Terminal";
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from "./ui/resizable";

// The session view: a focused overlay split into a resizable left info pane and
// a right "canvas" that hosts the terminal. Owns the session's data fetch and
// the kill/restore/rename/send actions (formerly the detail drawer).
export function SessionView(props: {
  sessionId: string;
  fallback: Session | null;
  projects: ProjectSummary[];
  onClose: () => void;
  onChanged: () => Promise<void> | void;
  onError: (msg: string) => void;
}) {
  const [session, setSession] = useState<Session | null>(props.fallback);
  const [message, setMessage] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [renaming, setRenaming] = useState(false);
  const [nameDraft, setNameDraft] = useState("");

  const refresh = useCallback(async () => {
    try {
      setSession(await getSession(props.sessionId));
    } catch (err) {
      props.onError(err instanceof Error ? err.message : String(err));
    }
  }, [props]);

  useEffect(() => {
    void refresh();
    const id = window.setInterval(() => void refresh(), 2000);
    return () => window.clearInterval(id);
  }, [refresh]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && props.onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [props]);

  const run = async (label: string, fn: () => Promise<void>) => {
    setBusy(label);
    try {
      await fn();
      await refresh();
      await props.onChanged();
    } catch (err) {
      props.onError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  };

  const s = session;
  const projectName =
    props.projects.find((p) => p.id === s?.projectId)?.name ?? s?.projectId;
  const title = s?.displayName || s?.id || props.sessionId;
  const connected = !!s && !s.isTerminated && s.activity?.state === "active";

  return (
    <div
      className="session-view-overlay"
      onClick={(e) => e.target === e.currentTarget && props.onClose()}
    >
      <div className="session-view">
        <header className="sv-head">
          {renaming ? (
            <input
              autoFocus
              className="sv-rename"
              value={nameDraft}
              onChange={(e) => setNameDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && nameDraft.trim()) {
                  void run("rename", () =>
                    renameSession(props.sessionId, nameDraft.trim()),
                  ).then(() => setRenaming(false));
                }
                if (e.key === "Escape") setRenaming(false);
              }}
            />
          ) : (
            <h3
              className="sv-title"
              onDoubleClick={() => {
                setNameDraft(s?.displayName ?? "");
                setRenaming(true);
              }}
              title="Double-click to rename"
            >
              {title}
            </h3>
          )}
          {s && (
            <span className={`badge tone-${statusMeta(s.status).tone}`}>
              <span className="bdot" />
              {statusMeta(s.status).label}
            </span>
          )}
          <div className="spacer" />
          <button className="icon-btn" onClick={props.onClose} title="Close (Esc)">
            ✕
          </button>
        </header>

        {!s ? (
          <div className="empty"><span className="spinner" /></div>
        ) : (
          <ResizablePanelGroup direction="horizontal" className="sv-body">
            <ResizablePanel defaultSize={38} minSize={26} className="sv-left">
              <dl className="kv">
                <Row k="ID" v={s.id} mono />
                <Row k="Project" v={projectName ?? "—"} />
                <Row k="Kind" v={s.kind} />
                <Row k="Harness" v={s.harness ?? "—"} mono />
                <Row k="Activity" v={s.activity?.state ?? "—"} mono />
                <Row k="Terminated" v={s.isTerminated ? "yes" : "no"} />
                {s.issueId && <Row k="Issue" v={s.issueId} mono />}
                <Row k="Created" v={fmtTime(s.createdAt)} mono />
                <Row k="Updated" v={fmtTime(s.updatedAt)} mono />
              </dl>

              <div className="drawer-section">
                <label className="section-label">Send message to agent</label>
                <textarea
                  placeholder="Type an instruction for the agent…"
                  value={message}
                  onChange={(e) => setMessage(e.target.value)}
                  disabled={s.isTerminated}
                />
                <div className="row-end">
                  <button
                    className="btn-primary sm"
                    disabled={!message.trim() || s.isTerminated || !!busy}
                    onClick={() =>
                      run("send", async () => {
                        await sendSessionMessage(props.sessionId, message.trim());
                        setMessage("");
                      })
                    }
                  >
                    {busy === "send" ? <span className="spinner" /> : "Send"}
                  </button>
                </div>
              </div>

              <div className="drawer-actions">
                {s.isTerminated ? (
                  <button
                    className="btn-ghost sm"
                    disabled={!!busy}
                    onClick={() => run("restore", () => restoreSession(props.sessionId).then())}
                  >
                    {busy === "restore" ? <span className="spinner" /> : "Restore"}
                  </button>
                ) : (
                  <button
                    className="btn-ghost sm danger"
                    disabled={!!busy}
                    onClick={() => run("kill", () => killSession(props.sessionId))}
                  >
                    {busy === "kill" ? <span className="spinner" /> : "Kill session"}
                  </button>
                )}
              </div>
            </ResizablePanel>

            <ResizableHandle withHandle />

            <ResizablePanel defaultSize={62} minSize={30} className="sv-canvas">
              <Terminal sessionId={s.id} connected={connected} />
            </ResizablePanel>
          </ResizablePanelGroup>
        )}
      </div>
    </div>
  );
}

function Row(props: { k: string; v: string; mono?: boolean }) {
  return (
    <>
      <dt>{props.k}</dt>
      <dd className={props.mono ? "mono" : ""}>{props.v}</dd>
    </>
  );
}
