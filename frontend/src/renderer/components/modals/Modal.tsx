import type { ReactNode } from "react";

export function Modal(props: { onClose: () => void; children: ReactNode }) {
  return (
    <div
      className="overlay"
      onClick={(e) => e.target === e.currentTarget && props.onClose()}
    >
      <div className="modal">{props.children}</div>
    </div>
  );
}
