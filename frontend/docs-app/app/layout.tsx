import type { Metadata } from "next";
import "../styles/globals.css";

export const metadata: Metadata = {
	title: "Agent Orchestrator",
	description: "Open-source platform for running parallel AI coding agents.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
	return (
		<html lang="en" suppressHydrationWarning>
			<body>{children}</body>
		</html>
	);
}
