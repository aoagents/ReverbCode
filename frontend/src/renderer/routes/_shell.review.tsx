import { createFileRoute } from "@tanstack/react-router";
import { ClipboardCheck } from "lucide-react";

export const Route = createFileRoute("/_shell/review")({
  component: ReviewRoute,
});

// Placeholder until the Go daemon grows a reviews API (Phase 4). The reviews
// board has no backend yet, so this route exists for nav parity and is filled
// in once the endpoints land.
function ReviewRoute() {
  return (
    <div className="flex h-full min-h-0 flex-col bg-background text-foreground">
      <header className="flex h-11 shrink-0 items-center gap-2.5 border-b border-border px-4">
        <ClipboardCheck className="h-[15px] w-[15px] shrink-0 text-accent" aria-hidden="true" />
        <span className="text-[13.5px] font-semibold text-foreground">Review</span>
      </header>
      <div className="grid min-h-0 flex-1 place-items-center p-6 text-center">
        <p className="max-w-sm text-[12px] text-passive">
          The code-review board needs a reviews API on the daemon. Coming once the backend lands.
        </p>
      </div>
    </div>
  );
}
