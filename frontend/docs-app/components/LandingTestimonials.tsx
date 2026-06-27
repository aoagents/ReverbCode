"use client";

import { useEffect, useState } from "react";

const testimonials = [
	{
		quote: "Set up 12 agents on our backlog before lunch. By end of day, 8 PRs were merged.",
		img: "https://i.pravatar.cc/120?img=13",
		name: "Staff Engineer",
		role: "Series B Startup",
	},
	{
		quote:
			"The auto CI recovery alone saves me hours a week. Agents fix their own broken tests. I just review and merge.",
		img: "https://i.pravatar.cc/120?img=32",
		name: "Solo Founder",
		role: "Indie SaaS",
	},
	{
		quote:
			"We went from 3 PRs/day to 15 PRs/day. The plugin system means we swapped in GitLab and Linear without changing our workflow.",
		img: "https://i.pravatar.cc/120?img=8",
		name: "Eng Lead",
		role: "20-person team",
	},
];

const ROTATE_MS = 5500;

export function LandingTestimonials() {
	const [active, setActive] = useState(0);
	const [show, setShow] = useState(true);
	const [paused, setPaused] = useState(false);

	const change = (next: number) => {
		if (next === active) return;
		setShow(false);
		window.setTimeout(() => {
			setActive(next);
			setShow(true);
		}, 240);
	};

	useEffect(() => {
		if (paused) return;
		const t = window.setTimeout(() => change((active + 1) % testimonials.length), ROTATE_MS);
		return () => window.clearTimeout(t);
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [active, paused]);

	const t = testimonials[active];

	return (
		<section className="py-20 px-6 pb-[120px] max-w-[72rem] mx-auto">
			<div className="landing-reveal">
				<div className="text-xs tracking-[0.15em] uppercase text-[var(--landing-muted)] opacity-60 mb-6">
					What engineers say
				</div>
				<h2 className="font-sans font-[680] tracking-tight font-normal text-[clamp(1.375rem,3vw,2rem)] leading-[1.05] tracking-[-1.5px]">
					Trusted by <em className="italic text-[var(--landing-muted)]">builders</em>
				</h2>
			</div>

			<div className="landing-reveal mt-16" onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}>
				{/* Quote — fades on change */}
				<div className="min-h-[8rem] max-w-[58rem]">
					<blockquote
						style={{
							opacity: show ? 1 : 0,
							transform: show ? "translateY(0)" : "translateY(8px)",
							transition: "opacity 0.4s ease, transform 0.4s cubic-bezier(0.22,1,0.36,1)",
						}}
						className="font-sans font-[680] tracking-tight text-[clamp(1.5rem,3.2vw,2.375rem)] leading-[1.3] tracking-[-0.75px] text-[var(--landing-fg)]"
					>
						&ldquo;{t.quote}&rdquo;
					</blockquote>
				</div>

				{/* Bottom row — author cluster on the left, step counter on the right */}
				<div className="flex items-center justify-between gap-6 mt-14 flex-wrap">
					<div className="flex items-center gap-5">
						<div className="flex items-center">
							{testimonials.map((item, i) => {
								const isActive = i === active;
								const size = isActive ? 56 : 44;
								return (
									<button
										key={item.name}
										onClick={() => change(i)}
										aria-label={`Show testimonial from ${item.name}`}
										aria-pressed={isActive}
										className="rounded-full overflow-hidden cursor-pointer shrink-0 p-0"
										style={{
											width: size,
											height: size,
											marginLeft: i === 0 ? 0 : -14,
											zIndex: isActive ? 30 : 10 - i,
											border: `2px solid ${isActive ? "var(--landing-accent)" : "var(--landing-card-bg)"}`,
											opacity: isActive ? 1 : 0.7,
											boxShadow: isActive ? "0 4px 16px rgba(0,0,0,0.35)" : "none",
											transition:
												"width 0.4s cubic-bezier(0.22,1,0.36,1), height 0.4s cubic-bezier(0.22,1,0.36,1), opacity 0.4s ease, border-color 0.4s ease",
										}}
									>
										{/* eslint-disable-next-line @next/next/no-img-element */}
										<img
											src={item.img}
											alt={item.name}
											width={size}
											height={size}
											className="w-full h-full object-cover"
											style={{
												filter: isActive ? "grayscale(0)" : "grayscale(1)",
												transition: "filter 0.4s ease",
											}}
										/>
									</button>
								);
							})}
						</div>

						{/* Vertical divider */}
						<div className="w-px h-10 shrink-0" style={{ background: "var(--landing-border-default)" }} />

						{/* Author — fades on change */}
						<div
							style={{
								opacity: show ? 1 : 0,
								transition: "opacity 0.4s ease 0.05s",
							}}
						>
							<div className="text-[0.9375rem] font-medium text-[var(--landing-fg)]">{t.name}</div>
							<div className="text-[0.8125rem] text-[var(--landing-muted)] opacity-60">{t.role}</div>
						</div>
					</div>

					{/* Step counter — fills the right side */}
					<div className="flex items-baseline gap-2 font-mono">
						<span
							className="text-[clamp(2.25rem,5vw,3.5rem)] font-[680] tracking-tight leading-none tabular-nums"
							style={{
								color: "var(--landing-fg)",
								opacity: show ? 1 : 0.4,
								transition: "opacity 0.3s ease",
							}}
						>
							{String(active + 1).padStart(2, "0")}
						</span>
						<span className="text-[var(--landing-muted)] opacity-40 text-lg">
							/ {String(testimonials.length).padStart(2, "0")}
						</span>
					</div>
				</div>
			</div>
		</section>
	);
}
