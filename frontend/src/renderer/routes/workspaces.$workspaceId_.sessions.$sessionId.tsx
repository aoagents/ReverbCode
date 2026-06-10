import { createFileRoute } from "@tanstack/react-router";
import { App } from "../App";

export const Route = createFileRoute("/workspaces/$workspaceId_/sessions/$sessionId")({
	component: SessionRoute,
});

function SessionRoute() {
	const { workspaceId, sessionId } = Route.useParams();
	return <App routeWorkspaceId={workspaceId} routeSessionId={sessionId} />;
}
