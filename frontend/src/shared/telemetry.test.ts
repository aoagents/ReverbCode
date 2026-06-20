import { mkdtemp, readFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { afterEach, expect, test } from "vitest";
import { buildTelemetryBootstrap, defaultDataDir, loadOrCreateTelemetryInstallId } from "./telemetry";

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
