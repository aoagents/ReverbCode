import { createFileRoute } from "@tanstack/react-router";
import { PullRequestsPage } from "../components/PullRequestsPage";

export const Route = createFileRoute("/_shell/prs")({
	component: PullRequestsPage,
});
