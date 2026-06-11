import { createFileRoute } from "@tanstack/react-router";
import { ReviewDashboard } from "../components/ReviewDashboard";

export const Route = createFileRoute("/_shell/review")({
	component: ReviewDashboard,
});
