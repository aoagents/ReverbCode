import { describe, expect, it, vi } from "vitest";
import {
	createListenPortScanner,
	defaultRunFilePath,
	isDaemonHealthz,
	parseDaemonListenPort,
	parseRunFile,
} from "./daemon-discovery";
import { daemonStatusEquals } from "./daemon-status";

// Real shape emitted by slog's TextHandler in backend/internal/httpd/server.go.
const LISTEN_LINE = 'time=2026-06-10T09:15:04.221-07:00 level=INFO msg="daemon listening" addr=127.0.0.1:3001 pid=4242';

describe("parseDaemonListenPort", () => {
	it("extracts the bound port from the slog listen line", () => {
		expect(parseDaemonListenPort(LISTEN_LINE)).toBe(3001);
	});

	it("reports a fallback port that differs from the configured one", () => {
		expect(parseDaemonListenPort(LISTEN_LINE.replace("3001", "3037"))).toBe(3037);
	});

	it("handles IPv6 listen addresses", () => {
		expect(parseDaemonListenPort('level=INFO msg="daemon listening" addr=[::1]:4317 pid=1')).toBe(4317);
	});

	it("ignores other log lines, even ones carrying an addr attribute", () => {
		expect(parseDaemonListenPort('level=INFO msg="proxy dial" addr=127.0.0.1:9999')).toBeNull();
		expect(parseDaemonListenPort("plain stdout chatter")).toBeNull();
	});

	it("returns null when the listen line has no usable addr", () => {
		expect(parseDaemonListenPort('level=INFO msg="daemon listening" pid=4242')).toBeNull();
		expect(parseDaemonListenPort('level=INFO msg="daemon listening" addr=garbage')).toBeNull();
		expect(parseDaemonListenPort('level=INFO msg="daemon listening" addr=127.0.0.1:99999')).toBeNull();
	});
});

describe("createListenPortScanner", () => {
	it("reassembles a line split across chunks", () => {
		const onPort = vi.fn();
		const scan = createListenPortScanner(onPort);

		scan('level=INFO msg="daemon lis');
		expect(onPort).not.toHaveBeenCalled();
		scan('tening" addr=127.0.0.1:3001 pid=4242\n');
		expect(onPort).toHaveBeenCalledWith(3001);
	});

	it("reports only the first announcement", () => {
		const onPort = vi.fn();
		const scan = createListenPortScanner(onPort);

		scan(`noise line\n${LISTEN_LINE}\n${LISTEN_LINE.replace("3001", "4000")}\n`);
		scan(`${LISTEN_LINE.replace("3001", "5000")}\n`);

		expect(onPort).toHaveBeenCalledTimes(1);
		expect(onPort).toHaveBeenCalledWith(3001);
	});

	it("stays quiet on streams without the announcement", () => {
		const onPort = vi.fn();
		const scan = createListenPortScanner(onPort);

		scan("starting up\nmigrations applied\n");
		expect(onPort).not.toHaveBeenCalled();
	});
});

describe("parseRunFile", () => {
	const valid = JSON.stringify({ pid: 4242, port: 3037, startedAt: "2026-06-10T16:15:04Z" });

	it("parses a valid handshake", () => {
		expect(parseRunFile(valid)).toEqual({
			pid: 4242,
			port: 3037,
			startedAtMs: Date.parse("2026-06-10T16:15:04Z"),
		});
	});

	it("returns null for malformed JSON", () => {
		expect(parseRunFile("{not json")).toBeNull();
		expect(parseRunFile("")).toBeNull();
		expect(parseRunFile("null")).toBeNull();
	});

	it("returns null for a missing or invalid port", () => {
		expect(parseRunFile(JSON.stringify({ pid: 1, startedAt: "2026-06-10T16:15:04Z" }))).toBeNull();
		expect(parseRunFile(JSON.stringify({ pid: 1, port: 0 }))).toBeNull();
		expect(parseRunFile(JSON.stringify({ pid: 1, port: 70000 }))).toBeNull();
		expect(parseRunFile(JSON.stringify({ pid: 1, port: "3001" }))).toBeNull();
	});

	it("treats a missing or unparseable startedAt as epoch zero", () => {
		expect(parseRunFile(JSON.stringify({ pid: 1, port: 3001 }))?.startedAtMs).toBe(0);
		expect(parseRunFile(JSON.stringify({ pid: 1, port: 3001, startedAt: "not-a-date" }))?.startedAtMs).toBe(0);
	});
});

describe("isDaemonHealthz", () => {
	it("accepts the daemon's healthz payload", () => {
		expect(isDaemonHealthz('{"pid":57329,"service":"agent-orchestrator-daemon","status":"ok"}')).toBe(true);
	});

	it("rejects foreign services squatting on a stale run-file port", () => {
		expect(isDaemonHealthz('{"service":"something-else","status":"ok"}')).toBe(false);
		expect(isDaemonHealthz('{"status":"ok"}')).toBe(false);
	});

	it("rejects non-ok status and malformed bodies", () => {
		expect(isDaemonHealthz('{"service":"agent-orchestrator-daemon","status":"degraded"}')).toBe(false);
		expect(isDaemonHealthz("<html>not json</html>")).toBe(false);
		expect(isDaemonHealthz("")).toBe(false);
		expect(isDaemonHealthz("null")).toBe(false);
	});
});

describe("daemonStatusEquals", () => {
	it("treats identical state, port, and message as equal", () => {
		expect(daemonStatusEquals({ state: "ready", port: 3001 }, { state: "ready", port: 3001 })).toBe(true);
		expect(daemonStatusEquals({ state: "stopped" }, { state: "stopped" })).toBe(true);
	});

	it("distinguishes differing state, port, or message", () => {
		expect(daemonStatusEquals({ state: "ready", port: 3001 }, { state: "ready", port: 3002 })).toBe(false);
		expect(daemonStatusEquals({ state: "stopped" }, { state: "ready", port: 3001 })).toBe(false);
		expect(daemonStatusEquals({ state: "stopped", message: "a" }, { state: "stopped", message: "b" })).toBe(false);
	});
});

describe("defaultRunFilePath", () => {
	it("mirrors Go os.UserConfigDir on macOS", () => {
		expect(defaultRunFilePath("darwin", {}, "/Users/me")).toBe(
			"/Users/me/Library/Application Support/agent-orchestrator/running.json",
		);
	});

	it("prefers XDG_CONFIG_HOME on linux, falling back to ~/.config", () => {
		expect(defaultRunFilePath("linux", { XDG_CONFIG_HOME: "/xdg" }, "/home/me")).toBe(
			"/xdg/agent-orchestrator/running.json",
		);
		expect(defaultRunFilePath("linux", {}, "/home/me")).toBe("/home/me/.config/agent-orchestrator/running.json");
	});

	it("uses APPDATA on windows and returns null when it is unset", () => {
		expect(defaultRunFilePath("win32", { APPDATA: "C:\\Users\\me\\AppData\\Roaming" }, "C:\\Users\\me")).toContain(
			"agent-orchestrator",
		);
		expect(defaultRunFilePath("win32", {}, "C:\\Users\\me")).toBeNull();
	});

	it("returns null when no config root can be resolved", () => {
		expect(defaultRunFilePath("linux", {}, "")).toBeNull();
		expect(defaultRunFilePath("darwin", {}, "")).toBeNull();
	});
});
