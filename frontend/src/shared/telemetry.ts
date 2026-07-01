import { mkdir, readFile, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { randomUUID } from "node:crypto";

// A user who returns on this many distinct days is very likely to be retained.
// Treated as the activation/retention magic number for the onboarding funnel.
export const RETENTION_MAGIC_NUMBER = 4;

export type TelemetryBootstrap = {
	distinctId: string;
	appVersion: string;
	platform: NodeJS.Platform;
	// First time the app has ever been launched on this machine (no prior launch
	// state persisted). Drives the funnel's first-launch stage.
	isFirstLaunch: boolean;
	// This launch is the first one on a new calendar day after install (a "return").
	isReturnDay: boolean;
	// Number of returns so far (distinct active days after the install day).
	returnCount: number;
	// Whether the user has reached the retention magic number of returns.
	isRetained: boolean;
	// Whole days elapsed since the install day.
	daysSinceInstall: number;
};

// Persisted launch history used to derive returns/retention. Renderer/app owned;
// the daemon never writes this file, so it is immune to the install-id race.
export type LaunchState = {
	installDay: string;
	lastActiveDay: string;
	distinctActiveDays: number;
};

export type LaunchOutcome = {
	isFirstLaunch: boolean;
	isReturnDay: boolean;
	returnCount: number;
	isRetained: boolean;
	daysSinceInstall: number;
};

// Local calendar date (YYYY-MM-DD) used as the unit for a distinct active day.
export function todayLocalDate(now = new Date()): string {
	const y = now.getFullYear();
	const m = `${now.getMonth() + 1}`.padStart(2, "0");
	const d = `${now.getDate()}`.padStart(2, "0");
	return `${y}-${m}-${d}`;
}

function dayDiff(fromDay: string, toDay: string): number {
	const from = Date.parse(`${fromDay}T00:00:00`);
	const to = Date.parse(`${toDay}T00:00:00`);
	if (Number.isNaN(from) || Number.isNaN(to)) return 0;
	return Math.max(0, Math.round((to - from) / 86_400_000));
}

// Pure transition: given the previous launch state (or null on first ever launch)
// and today's date, returns the next state to persist and the funnel outcome.
// Install day counts as the first distinct active day but is NOT a return; each
// later distinct day increments returnCount. Retention triggers at the magic number.
export function computeLaunchUpdate(prev: LaunchState | null, today: string): { next: LaunchState; outcome: LaunchOutcome } {
	if (!prev) {
		return {
			next: { installDay: today, lastActiveDay: today, distinctActiveDays: 1 },
			outcome: { isFirstLaunch: true, isReturnDay: false, returnCount: 0, isRetained: false, daysSinceInstall: 0 },
		};
	}
	if (today === prev.lastActiveDay) {
		const returnCount = Math.max(0, prev.distinctActiveDays - 1);
		return {
			next: prev,
			outcome: {
				isFirstLaunch: false,
				isReturnDay: false,
				returnCount,
				isRetained: returnCount >= RETENTION_MAGIC_NUMBER,
				daysSinceInstall: dayDiff(prev.installDay, today),
			},
		};
	}
	const distinctActiveDays = prev.distinctActiveDays + 1;
	const returnCount = distinctActiveDays - 1;
	return {
		next: { installDay: prev.installDay, lastActiveDay: today, distinctActiveDays },
		outcome: {
			isFirstLaunch: false,
			isReturnDay: true,
			returnCount,
			isRetained: returnCount >= RETENTION_MAGIC_NUMBER,
			daysSinceInstall: dayDiff(prev.installDay, today),
		},
	};
}

export function defaultDataDir(
	platform: NodeJS.Platform,
	env: Record<string, string | undefined>,
	homeDir: string,
): string | null {
	void platform;
	if (env.AO_DATA_DIR) return env.AO_DATA_DIR;
	if (!homeDir) return null;
	return path.join(homeDir, ".ao", "data");
}

export async function loadOrCreateTelemetryInstallId(dataDir: string): Promise<string> {
	const file = path.join(dataDir, "telemetry_install_id");
	try {
		const existing = (await readFile(file, "utf8")).trim();
		if (existing) return existing;
	} catch {
		// Create the id on first use.
	}
	await mkdir(dataDir, { recursive: true });
	const distinctId = `ins_${randomUUID()}`;
	await writeFile(file, `${distinctId}\n`, { mode: 0o600 });
	return distinctId;
}

async function loadLaunchState(dataDir: string): Promise<LaunchState | null> {
	const file = path.join(dataDir, "telemetry_app_launches.json");
	try {
		const parsed = JSON.parse(await readFile(file, "utf8")) as Partial<LaunchState>;
		if (
			typeof parsed.installDay === "string" &&
			typeof parsed.lastActiveDay === "string" &&
			typeof parsed.distinctActiveDays === "number"
		) {
			return { installDay: parsed.installDay, lastActiveDay: parsed.lastActiveDay, distinctActiveDays: parsed.distinctActiveDays };
		}
	} catch {
		// Missing or unreadable file means this is treated as the first launch.
	}
	return null;
}

async function saveLaunchState(dataDir: string, state: LaunchState): Promise<void> {
	await mkdir(dataDir, { recursive: true });
	await writeFile(path.join(dataDir, "telemetry_app_launches.json"), `${JSON.stringify(state)}\n`, { mode: 0o600 });
}

export async function buildTelemetryBootstrap(
	env: Record<string, string | undefined>,
	appVersion: string,
	platform: NodeJS.Platform,
	homeDir = os.homedir(),
	now = new Date(),
): Promise<TelemetryBootstrap | null> {
	const dataDir = defaultDataDir(platform, env, homeDir);
	if (!dataDir) return null;
	const distinctId = await loadOrCreateTelemetryInstallId(dataDir);
	const prev = await loadLaunchState(dataDir);
	const { next, outcome } = computeLaunchUpdate(prev, todayLocalDate(now));
	if (!prev || next !== prev) {
		await saveLaunchState(dataDir, next);
	}
	return {
		distinctId,
		appVersion,
		platform,
		...outcome,
	};
}
