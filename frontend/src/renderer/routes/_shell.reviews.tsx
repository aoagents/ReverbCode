import { createFileRoute, redirect } from "@tanstack/react-router";

// /reviews is an alias for /review (matches agent-orchestrator).
export const Route = createFileRoute("/_shell/reviews")({
	beforeLoad: () => {
		throw redirect({ to: "/review" });
	},
});
