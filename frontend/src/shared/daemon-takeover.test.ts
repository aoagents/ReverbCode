// Unit tests for shouldReplacePortHolder. Run with:
//   cd frontend && npx vitest run src/shared/daemon-takeover.test.ts
import { describe, expect, it } from "vitest";
import { DAEMON_SERVICE_NAME, type DaemonProbe } from "./daemon-attach";
import { shouldReplacePortHolder } from "./daemon-takeover";

// A minimal valid DaemonProbe (non-null means the AO daemon answered /healthz).
const healthyProbe: DaemonProbe = {
	status: "ok",
	service: DAEMON_SERVICE_NAME,
	pid: 1234,
};

describe("shouldReplacePortHolder", () => {
	it("returns true when a probe answered (rejected responder, any holderPidAlive)", () => {
		expect(shouldReplacePortHolder(healthyProbe, false)).toBe(true);
		expect(shouldReplacePortHolder(healthyProbe, true)).toBe(true);
	});

	it("returns true when probe is null but the run-file PID is still alive (hung holder)", () => {
		expect(shouldReplacePortHolder(null, true)).toBe(true);
	});

	it("returns false when probe is null and no live holder PID (nothing to kill)", () => {
		expect(shouldReplacePortHolder(null, false)).toBe(false);
	});
});
