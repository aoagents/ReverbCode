import { createFileRoute } from "@tanstack/react-router";
import { App } from "../App";

export const Route = createFileRoute("/workspaces/$workspaceId")({
	component: WorkspaceRoute,
});

function WorkspaceRoute() {
	const { workspaceId } = Route.useParams();
	return <App routeWorkspaceId={workspaceId} />;
}
