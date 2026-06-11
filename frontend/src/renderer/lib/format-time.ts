/** Compact relative time — ported from agent-orchestrator session-detail-utils. */
export function formatTimeCompact(isoDate: string | null | undefined): string {
	if (!isoDate) return "just now";
	const ts = new Date(isoDate).getTime();
	if (!Number.isFinite(ts)) return "just now";
	const diffMs = Date.now() - ts;
	if (diffMs <= 0) return "just now";
	const diffMins = Math.floor(diffMs / 60000);
	const diffHours = Math.floor(diffMins / 60);
	if (diffMins < 1) return "just now";
	if (diffMins < 60) return `${diffMins}m ago`;
	if (diffHours < 24) return `${diffHours}h ago`;
	return `${Math.floor(diffHours / 24)}d ago`;
}
