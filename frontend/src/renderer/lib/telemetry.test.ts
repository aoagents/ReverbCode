import { describe, expect, it } from "vitest";
import {
	routeSurface,
	sanitizeRendererExceptionProperties,
	sanitizeRendererProperties,
} from "./telemetry";

describe("telemetry sanitizers", () => {
	it("categorizes routes without exporting raw paths", () => {
		expect(routeSurface("/")).toBe("home");
		expect(routeSurface("/projects/demo")).toBe("project_board");
		expect(routeSurface("/projects/demo/settings")).toBe("project_settings");
		expect(routeSurface("/projects/demo/sessions/demo-1")).toBe("session_detail");
		expect(routeSurface("/prs")).toBe("pull_requests");
	});

	it("hashes renderer ids and drops raw route identifiers", async () => {
		const props = await sanitizeRendererProperties("ao.renderer.project_removed", { project_id: "demo-project" });
		expect(props).toHaveProperty("project_id_hash");
		expect(props).not.toHaveProperty("project_id");

		const routeProps = await sanitizeRendererProperties("ao.renderer.route_viewed", {
			surface: "project_board",
			pathname: "/projects/demo",
			search: "?token=secret",
		});
		expect(routeProps).toEqual({ surface: "project_board" });
	});

	it("strips exception details down to coarse metadata", async () => {
		const props = await sanitizeRendererExceptionProperties(new TypeError("local path /tmp/private"), {
			source: "window-error",
			unhandled: true,
			project_id: "demo-project",
			component_stack: "App > Shell",
		});
		expect(props).toMatchObject({
			error_name: "TypeError",
			source: "window-error",
			unhandled: true,
		});
		expect(props).toHaveProperty("project_id_hash");
		expect(props).not.toHaveProperty("project_id");
		expect(props).not.toHaveProperty("component_stack");
	});
});
