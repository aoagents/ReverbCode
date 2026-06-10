// Connection state of the daemon's SSE event stream, kept as a tiny external
// store (useSyncExternalStore-compatible) so the transport — which lives
// outside React — can drive UI signals like the sidebar's "events offline".
export type EventsConnectionState =
	| "idle" // no stream (transport not connected, or torn down)
	| "connected" // stream open; live updates flowing
	| "disconnected"; // stream lost; UI state may be stale until it reconnects

let state: EventsConnectionState = "idle";
const listeners = new Set<() => void>();

export function getEventsConnectionState(): EventsConnectionState {
	return state;
}

export function setEventsConnectionState(next: EventsConnectionState): void {
	if (next === state) return;
	state = next;
	listeners.forEach((listener) => listener());
}

export function subscribeEventsConnection(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}
