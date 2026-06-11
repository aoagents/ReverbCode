import type { ITheme } from "@xterm/xterm";

/** xterm palettes harmonized to styles.css tokens (agent-orchestrator pattern). */
export function buildTerminalThemes(): { dark: ITheme; light: ITheme } {
	const accent = {
		cursor: "#f59f4c",
		selDark: "rgba(77, 141, 255, 0.30)",
		selLight: "rgba(37, 99, 235, 0.25)",
	};

	const dark: ITheme = {
		background: "#15171b",
		foreground: "#d7d7d2",
		cursor: accent.cursor,
		cursorAccent: "#15171b",
		selectionBackground: accent.selDark,
		selectionInactiveBackground: "rgba(128, 128, 128, 0.2)",
		black: "#15171b",
		red: "#ef6b6b",
		green: "#74b98a",
		yellow: "#e8c14a",
		blue: "#4d8dff",
		magenta: "#a78bfa",
		cyan: "#6fb3c9",
		white: "#d7d7d2",
		brightBlack: "#7c7c7c",
		brightRed: "#ff8a8a",
		brightGreen: "#8fd6a6",
		brightYellow: "#f0d06b",
		brightBlue: "#7eaaff",
		brightMagenta: "#c4b0fc",
		brightCyan: "#8fcfe0",
		brightWhite: "#f4f5f7",
	};

	const light: ITheme = {
		background: "#fafafa",
		foreground: "#24292f",
		cursor: accent.cursor,
		cursorAccent: "#fafafa",
		selectionBackground: accent.selLight,
		selectionInactiveBackground: "rgba(128, 128, 128, 0.15)",
		black: "#24292f",
		red: "#b42318",
		green: "#1a7f37",
		yellow: "#9a6b00",
		blue: "#2563eb",
		magenta: "#8e24aa",
		cyan: "#0b7285",
		white: "#4b5563",
		brightBlack: "#374151",
		brightRed: "#912018",
		brightGreen: "#176639",
		brightYellow: "#6f4a00",
		brightBlue: "#1d4ed8",
		brightMagenta: "#7b1fa2",
		brightCyan: "#155e75",
		brightWhite: "#374151",
	};

	return { dark, light };
}
