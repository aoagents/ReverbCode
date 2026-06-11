import { createFileRoute } from "@tanstack/react-router";
import { ProjectSettingsForm } from "../components/ProjectSettingsForm";

export const Route = createFileRoute("/_shell/projects/$projectId_/settings")({
	component: ProjectSettingsRoute,
});

function ProjectSettingsRoute() {
	const { projectId } = Route.useParams();
	return <ProjectSettingsForm projectId={projectId} />;
}
