import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import { setApiBaseUrl } from "../lib/api-client";
import { useWorkspaceQuery } from "./useWorkspaceQuery";

function wrapper(props: { children: ReactNode }) {
	// The hook pins its own retry policy; retryDelay 0 keeps the error tests fast.
	const queryClient = new QueryClient({ defaultOptions: { queries: { retryDelay: 0 } } });

	return <QueryClientProvider client={queryClient}>{props.children}</QueryClientProvider>;
}

type TestProject = {
	id: string;
	name: string;
	path: string;
};

type TestSessionPR = {
	number: number;
	state: string;
	url: string;
	ci: string;
	review: string;
	mergeability: string;
	reviewComments: boolean;
	updatedAt: string;
};

type TestSession = {
	id: string;
	projectId: string;
	terminalHandleId?: string;
	displayName?: string;
	harness?: string;
	branch?: string;
	status?: string;
	isTerminated: boolean;
	updatedAt: string;
	prs?: TestSessionPR[];
};

type TestErrorBody = {
	error: string;
	code: string;
	message: string;
	requestId: string;
};

type EndpointResponse = {
	status?: number;
	body?: { projects: TestProject[] } | { sessions: TestSession[] } | TestErrorBody;
	rejectWith?: Error;
};

function respondWith(
	projectsResponse: EndpointResponse | undefined,
	sessionsResponse: EndpointResponse | undefined,
) {
	return vi.spyOn(globalThis, "fetch").mockImplementation(async (input: RequestInfo | URL) => {
		const url = input instanceof Request ? input.url : input.toString();

		if (url.endsWith("/api/v1/projects")) {
			if (projectsResponse?.rejectWith) {
				throw projectsResponse.rejectWith;
			}

			return new Response(JSON.stringify(projectsResponse?.body ?? { projects: [] }), {
				status: projectsResponse?.status ?? 200,
				headers: { "Content-Type": "application/json" },
			});
		}

		if (url.endsWith("/api/v1/sessions")) {
			if (sessionsResponse?.rejectWith) {
				throw sessionsResponse.rejectWith;
			}

			return new Response(JSON.stringify(sessionsResponse?.body ?? { sessions: [] }), {
				status: sessionsResponse?.status ?? 200,
				headers: { "Content-Type": "application/json" },
			});
		}

		throw new Error(`unexpected GET ${url}`);
	});
}

beforeEach(() => {
	setApiBaseUrl("http://127.0.0.1:3001");
});

afterEach(() => {
	vi.restoreAllMocks();
	setApiBaseUrl("http://127.0.0.1:3001");
});

describe("useWorkspaceQuery", () => {
	it("returns an empty workspace list while the daemon base URL is untrusted", async () => {
		const fetchSpy = vi.spyOn(globalThis, "fetch");
		setApiBaseUrl(null);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isSuccess).toBe(true));
		expect(result.current.data).toEqual([]);
		expect(fetchSpy).not.toHaveBeenCalled();
	});

	it("maps projects and their sessions, applying provider/status/title fallbacks", async () => {
		respondWith(
			{
				body: { projects: [{ id: "proj-1", name: "my-app", path: "/home/me/my-app" }] },
			},
			{
				body: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							terminalHandleId: "term-1",
							displayName: "fix-bug",
							harness: "claude-code",
							branch: "qa/modal-worker",
							status: "mergeable",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						{
							// Unknown harness/status and no displayName/issueId: falls back
							// to codex / unknown / the session id.
							id: "sess-2",
							projectId: "proj-1",
							harness: "mystery-agent",
							status: "bogus",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
						// Belongs to another project; must not leak into proj-1.
						{ id: "sess-3", projectId: "proj-2", isTerminated: false, updatedAt: "2026-06-10T16:15:04Z" },
					],
				},
			},
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const [workspace] = result.current.data ?? [];
		expect(workspace).toMatchObject({ id: "proj-1", name: "my-app", path: "/home/me/my-app" });
		expect(workspace.sessions).toHaveLength(2);
		expect(workspace.sessions[0]).toMatchObject({
			id: "sess-1",
			terminalHandleId: "term-1",
			title: "fix-bug",
			provider: "claude-code",
			branch: "qa/modal-worker",
			status: "mergeable",
		});
		expect(workspace.sessions[1]).toMatchObject({
			id: "sess-2",
			title: "sess-2",
			provider: "codex",
			status: "unknown",
			branch: "session/sess-2",
		});
	});

	it("maps each session's prs straight from the session list", async () => {
		respondWith(
			{ body: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] } },
			{
				body: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "pr_open",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
							prs: [
								{
									number: 278,
									state: "open",
									url: "u",
									ci: "passing",
									review: "approved",
									mergeability: "clean",
									reviewComments: false,
									updatedAt: "2026-06-10T16:15:04Z",
								},
							],
						},
						{
							id: "sess-2",
							projectId: "proj-1",
							status: "working",
							isTerminated: false,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
			},
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		const sessions = result.current.data?.[0].sessions ?? [];
		expect(sessions[0].prs).toEqual([
			{
				number: 278,
				state: "open",
				url: "u",
				ci: "passing",
				review: "approved",
				mergeability: "clean",
				reviewComments: false,
				updatedAt: "2026-06-10T16:15:04Z",
			},
		]);
		// A session with no PRs maps to an empty stack, so the empty states render.
		expect(sessions[1].prs).toEqual([]);
	});

	it("preserves backend merged status for terminated merged sessions", async () => {
		respondWith(
			{ body: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] } },
			{
				body: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "merged",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
			},
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("merged");
	});

	it("falls back to terminated for terminated sessions without a known backend status", async () => {
		respondWith(
			{ body: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] } },
			{
				body: {
					sessions: [
						{
							id: "sess-1",
							projectId: "proj-1",
							status: "bogus",
							isTerminated: true,
							updatedAt: "2026-06-10T16:15:04Z",
						},
					],
				},
			},
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });
		await waitFor(() => expect(result.current.isSuccess).toBe(true));

		expect(result.current.data?.[0].sessions[0].status).toBe("terminated");
	});

	it("surfaces a projects fetch error", async () => {
		const failure = new TypeError("Failed to fetch");
		respondWith({ rejectWith: failure }, undefined);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("surfaces a sessions fetch error even when projects load", async () => {
		const failure = new Error("sessions backend down");
		respondWith(
			{ body: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] } },
			{ rejectWith: failure },
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBe(failure);
	});

	it("converts plain openapi-fetch error bodies into Error instances with the backend message", async () => {
		respondWith(
			{ body: { projects: [{ id: "proj-1", name: "my-app", path: "/p" }] } },
			{
				status: 500,
				body: {
					error: "Internal server error",
					code: "internal_error",
					message: "failed to list sessions: scan created_at",
					requestId: "req-123",
				},
			},
		);

		const { result } = renderHook(() => useWorkspaceQuery(), { wrapper });

		await waitFor(() => expect(result.current.isError).toBe(true), { timeout: 3_000 });
		expect(result.current.error).toBeInstanceOf(Error);
		expect(result.current.error?.message).toBe("failed to list sessions: scan created_at");
	});
});
