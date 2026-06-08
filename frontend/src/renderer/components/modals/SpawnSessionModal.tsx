import { useState } from "react";
import {
  spawnSession,
  type AgentHarness,
  type ProjectSummary,
  type SessionKind,
} from "../../lib/api";
import { Modal } from "./Modal";

const HARNESSES: AgentHarness[] = ["claude-code", "codex", "aider", "opencode"];
const KINDS: SessionKind[] = ["worker", "orchestrator"];

export function SpawnSessionModal(props: {
  projects: ProjectSummary[];
  defaultProject: string | null;
  onClose: () => void;
  onDone: () => void;
}) {
  const [projectId, setProjectId] = useState(
    props.defaultProject ?? props.projects[0]?.id ?? "",
  );
  const [kind, setKind] = useState<SessionKind>("worker");
  const [harness, setHarness] = useState<AgentHarness>("claude-code");
  const [branch, setBranch] = useState("");
  const [prompt, setPrompt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    if (!projectId) {
      setError("select a project");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await spawnSession({
        projectId,
        kind,
        harness,
        branch: branch.trim() || undefined,
        prompt: prompt.trim() || undefined,
      });
      props.onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <Modal onClose={props.onClose}>
      <h3>New agent</h3>
      <div className="modal-sub">Start an agent working on a project.</div>
      <div className="field">
        <label>Project</label>
        <select value={projectId} onChange={(e) => setProjectId(e.target.value)}>
          {props.projects.map((p) => (
            <option key={p.id} value={p.id}>
              {p.name} ({p.id})
            </option>
          ))}
        </select>
      </div>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
        <div className="field">
          <label>Harness</label>
          <select value={harness} onChange={(e) => setHarness(e.target.value as AgentHarness)}>
            {HARNESSES.map((h) => (
              <option key={h} value={h}>{h}</option>
            ))}
          </select>
        </div>
        <div className="field">
          <label>Kind</label>
          <select value={kind} onChange={(e) => setKind(e.target.value as SessionKind)}>
            {KINDS.map((k) => (
              <option key={k} value={k}>{k}</option>
            ))}
          </select>
        </div>
      </div>
      <div className="field">
        <label>Branch (optional)</label>
        <input placeholder="auto if blank" value={branch} onChange={(e) => setBranch(e.target.value)} />
      </div>
      <div className="field">
        <label>Prompt (optional)</label>
        <textarea
          placeholder="Initial instruction for the agent…"
          value={prompt}
          onChange={(e) => setPrompt(e.target.value)}
        />
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="modal-actions">
        <button className="btn-ghost" onClick={props.onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <span className="spinner" /> : "Spawn agent"}
        </button>
      </div>
    </Modal>
  );
}
