import { mkdtemp, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, expect, test } from "vitest";
import {
	buildTelemetryBootstrap,
	computeLaunchUpdate,
	defaultDataDir,
	loadOrCreateTelemetryInstallId,
	RETENTION_MAGIC_NUMBER,
	todayLocalDate,
} from "./telemetry";

const tempDirs: string[] = [];

afterEach(async () => {
	await Promise.all(
		tempDirs
			.splice(0)
			.map((dir) => import("node:fs/promises").then(({ rm }) => rm(dir, { recursive: true, force: true }))),
	);
});

test("defaultDataDir prefers AO_DATA_DIR", () => {
	expect(defaultDataDir("linux", { AO_DATA_DIR: "/tmp/custom" }, "/home/test")).toBe("/tmp/custom");
});

test("loadOrCreateTelemetryInstallId persists a stable install id", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);

	const first = await loadOrCreateTelemetryInstallId(dir);
	const second = await loadOrCreateTelemetryInstallId(dir);
	const stored = (await readFile(path.join(dir, "telemetry_install_id"), "utf8")).trim();

	expect(first).toMatch(/^ins_/);
	expect(second).toBe(first);
	expect(stored).toBe(first);
});

test("buildTelemetryBootstrap returns null when no home dir is available", async () => {
	await expect(buildTelemetryBootstrap({}, "1.2.3", "linux", "")).resolves.toBeNull();
});

test("computeLaunchUpdate flags the first ever launch and emits no return", () => {
	const { next, outcome } = computeLaunchUpdate(null, "2026-06-29");
	expect(outcome).toMatchObject({
		isFirstLaunch: true,
		isReturnDay: false,
		returnCount: 0,
		isRetained: false,
		daysSinceInstall: 0,
	});
	expect(next).toEqual({ installDay: "2026-06-29", lastActiveDay: "2026-06-29", distinctActiveDays: 1 });
});

test("computeLaunchUpdate ignores same-day relaunches", () => {
	const prev = { installDay: "2026-06-29", lastActiveDay: "2026-06-29", distinctActiveDays: 1 };
	const { next, outcome } = computeLaunchUpdate(prev, "2026-06-29");
	expect(outcome.isReturnDay).toBe(false);
	expect(outcome.returnCount).toBe(0);
	expect(next).toBe(prev);
});

test("computeLaunchUpdate counts a new calendar day as a return", () => {
	const prev = { installDay: "2026-06-29", lastActiveDay: "2026-06-29", distinctActiveDays: 1 };
	const { next, outcome } = computeLaunchUpdate(prev, "2026-06-30");
	expect(outcome).toMatchObject({
		isFirstLaunch: false,
		isReturnDay: true,
		returnCount: 1,
		isRetained: false,
		daysSinceInstall: 1,
	});
	expect(next.distinctActiveDays).toBe(2);
});

test("computeLaunchUpdate marks retention at the magic number of returns", () => {
	// install day + RETENTION_MAGIC_NUMBER additional distinct days = retained.
	const prev = { installDay: "2026-06-01", lastActiveDay: "2026-06-04", distinctActiveDays: RETENTION_MAGIC_NUMBER };
	const { outcome } = computeLaunchUpdate(prev, "2026-06-05");
	expect(outcome.returnCount).toBe(RETENTION_MAGIC_NUMBER);
	expect(outcome.isRetained).toBe(true);
});

test("todayLocalDate formats a zero-padded local date", () => {
	expect(todayLocalDate(new Date(2026, 0, 5, 10, 30))).toBe("2026-01-05");
});

test("buildTelemetryBootstrap persists launch state and detects returns across days", async () => {
	const dir = await mkdtemp(path.join(os.tmpdir(), "ao-telemetry-"));
	tempDirs.push(dir);
	const env = { AO_DATA_DIR: dir };

	const first = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", new Date(2026, 5, 29));
	expect(first?.isFirstLaunch).toBe(true);
	expect(first?.isReturnDay).toBe(false);
	expect(first?.distinctId).toMatch(/^ins_/);

	const sameDay = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", new Date(2026, 5, 29));
	expect(sameDay?.isFirstLaunch).toBe(false);
	expect(sameDay?.isReturnDay).toBe(false);

	const nextDay = await buildTelemetryBootstrap(env, "1.2.3", "linux", "/home/test", new Date(2026, 5, 30));
	expect(nextDay?.isReturnDay).toBe(true);
	expect(nextDay?.returnCount).toBe(1);

	const persisted = JSON.parse(await readFile(path.join(dir, "telemetry_app_launches.json"), "utf8"));
	expect(persisted.distinctActiveDays).toBe(2);
	expect(persisted.installDay).toBe("2026-06-29");
});
