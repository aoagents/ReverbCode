interface LandingHeroProps {
	starsLabel: string;
}

export function LandingHero({ starsLabel }: LandingHeroProps) {
	return (
		<div className="relative min-h-screen overflow-hidden">
			<section className="relative z-10 flex flex-col items-center justify-center text-center px-6 pt-32 pb-20 min-h-screen">
				<div className="landing-fade-rise landing-card inline-flex items-center gap-2 rounded-lg px-3 py-1.5 text-xs text-[var(--landing-muted)] mb-8">
					<span className="w-1.5 h-1.5 rounded-full bg-[rgba(134,239,172,0.7)]" />
					Open Source · MIT Licensed · {starsLabel} GitHub Stars
				</div>
				<h1 className="landing-fade-rise font-sans font-[680] text-[clamp(1.75rem,4vw,2.75rem)] leading-[1] tracking-[-2px] max-w-[56rem]">
					Run 30 AI agents in parallel.
					<br />
					<span className="text-[var(--landing-muted)]">One dashboard.</span>
				</h1>
				<p className="landing-fade-rise-d1 text-[var(--landing-muted)] text-[0.9375rem] max-w-[38rem] mt-6 leading-[1.7]">
					Agent Orchestrator spawns Claude Code, Codex, Cursor, Aider, and OpenCode in isolated git worktrees. Each
					agent gets its own branch, creates PRs, fixes CI, and addresses reviews autonomously.
				</p>
				<div className="landing-fade-rise-d2 flex items-center gap-3 mt-10 flex-wrap justify-center">
					<div className="landing-card rounded-lg px-6 py-3 font-mono text-sm">
						<span className="text-[var(--landing-muted)] opacity-40">$</span> npx @aoagents/ao start
					</div>
					<a
						href="/docs"
						className="landing-card rounded-lg px-6 py-3 text-sm no-underline transition-colors hover:text-white"
					>
						Read Docs
					</a>
					<a
						href="https://github.com/ComposioHQ/agent-orchestrator"
						target="_blank"
						rel="noopener noreferrer"
						className="liquid-glass-solid rounded-lg px-6 py-3 text-sm no-underline transition-colors"
					>
						View on GitHub
					</a>
				</div>

				<div className="landing-fade-rise-d2 w-full max-w-[72rem] mt-16">
					<div style={{ maxWidth: "62rem", margin: "0 auto" }}>
						{/* Laptop screen / lid */}
						<div
							className="rounded-[14px]"
							style={{
								padding: 10,
								background: "linear-gradient(180deg, #211f1c 0%, #161513 100%)",
								border: "1px solid var(--landing-border-default)",
								boxShadow: "0 30px 70px -24px rgba(0,0,0,0.65)",
							}}
						>
							<div
								className="overflow-hidden"
								style={{
									borderRadius: 6,
									aspectRatio: "16 / 10",
									background: "#0c0b0a",
								}}
							>
								{/* eslint-disable-next-line @next/next/no-img-element */}
								<img
									src="/hero-dashboard.png"
									alt="Agent Orchestrator dashboard — live agent sessions flowing from work to review to merge"
									className="w-full h-full"
									style={{ objectFit: "cover", objectPosition: "top", display: "block" }}
								/>
							</div>
						</div>
						{/* Laptop base / hinge */}
						<div style={{ width: "112%", marginLeft: "-6%" }}>
							<div
								style={{
									height: 16,
									background: "linear-gradient(180deg, #2a2823 0%, #131210 100%)",
									borderRadius: "0 0 12px 12px",
									borderTop: "1px solid var(--landing-border-default)",
									position: "relative",
								}}
							>
								<div
									style={{
										position: "absolute",
										top: 0,
										left: "50%",
										transform: "translateX(-50%)",
										width: "15%",
										height: 6,
										background: "#0c0b0a",
										borderRadius: "0 0 8px 8px",
									}}
								/>
							</div>
						</div>
					</div>
				</div>
			</section>
		</div>
	);
}
