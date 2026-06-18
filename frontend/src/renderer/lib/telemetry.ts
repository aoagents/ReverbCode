import posthog from "posthog-js/dist/module.full.no-external";
import { aoBridge } from "./bridge";
import { DEFAULT_POSTHOG_HOST, DEFAULT_POSTHOG_PROJECT_KEY } from "../../shared/posthog-config";

const POSTHOG_KEY = import.meta.env.VITE_AO_POSTHOG_KEY?.trim() || DEFAULT_POSTHOG_PROJECT_KEY;
const POSTHOG_HOST = import.meta.env.VITE_AO_POSTHOG_HOST?.trim() || DEFAULT_POSTHOG_HOST;
const RELEASE_TAG = "2026-01-30";

let initPromise: Promise<boolean> | null = null;
let errorHandlersBound = false;

function normalizeException(reason: unknown): Error {
	if (reason instanceof Error) return reason;
	if (typeof reason === "string") return new Error(reason);
	try {
		return new Error(JSON.stringify(reason));
	} catch {
		return new Error("Unknown renderer exception");
	}
}

function bindErrorHandlers() {
	if (errorHandlersBound) return;
	errorHandlersBound = true;
	window.addEventListener("error", (event) => {
		posthog.captureException(event.error ?? new Error(event.message), {
			source: "renderer",
			filename: event.filename,
			lineno: event.lineno,
			colno: event.colno,
		});
	});
	window.addEventListener("unhandledrejection", (event) => {
		posthog.captureException(normalizeException(event.reason), {
			source: "renderer",
			unhandled: true,
		});
	});
}

export async function initTelemetry(): Promise<boolean> {
	if (initPromise) return initPromise;
	initPromise = (async () => {
		if (!POSTHOG_KEY) return false;
		const bootstrap = await aoBridge.telemetry.getBootstrap();
		if (!bootstrap) return false;
		posthog.init(POSTHOG_KEY, {
			api_host: POSTHOG_HOST,
			defaults: RELEASE_TAG,
			autocapture: false,
			capture_pageview: false,
			persistence: "localStorage",
		});
		posthog.identify(bootstrap.distinctId, {
			app_version: bootstrap.appVersion,
			platform: bootstrap.platform,
			surface: "renderer",
		});
		posthog.register({
			app_version: bootstrap.appVersion,
			platform: bootstrap.platform,
			surface: "renderer",
			build_mode: import.meta.env.DEV ? "dev" : "packaged",
		});
		bindErrorHandlers();
		posthog.capture("ao.app.active", { channel: "renderer" });
		posthog.capture("ao.renderer.loaded");
		return true;
	})().catch(() => false);
	return initPromise;
}

export async function captureRendererEvent(event: string, properties?: Record<string, unknown>): Promise<void> {
	if (!(await initTelemetry())) return;
	posthog.capture(event, properties);
}

export async function captureRendererException(
	error: unknown,
	properties?: Record<string, unknown>,
): Promise<void> {
	if (!(await initTelemetry())) return;
	posthog.captureException(normalizeException(error), properties);
}
