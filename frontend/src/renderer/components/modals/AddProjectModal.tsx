import { useState } from "react";
import { addProject } from "../../lib/api";
import { Modal } from "./Modal";

export function AddProjectModal(props: { onClose: () => void; onDone: () => void }) {
  const [path, setPath] = useState("");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async () => {
    if (!path.trim()) {
      setError("path is required");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await addProject({ path: path.trim(), name: name.trim() || undefined });
      props.onDone();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      setBusy(false);
    }
  };

  return (
    <Modal onClose={props.onClose}>
      <h3>Add project</h3>
      <div className="modal-sub">
        Register a local git repository to run agents against.
      </div>
      <div className="field">
        <label>Repository path</label>
        <input
          autoFocus
          placeholder="/home/you/code/my-repo"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
      </div>
      <div className="field">
        <label>Display name (optional)</label>
        <input
          placeholder="derived from path if blank"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => e.key === "Enter" && submit()}
        />
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="modal-actions">
        <button className="btn-ghost" onClick={props.onClose} disabled={busy}>
          Cancel
        </button>
        <button className="btn-primary" onClick={submit} disabled={busy}>
          {busy ? <span className="spinner" /> : "Add project"}
        </button>
      </div>
    </Modal>
  );
}
