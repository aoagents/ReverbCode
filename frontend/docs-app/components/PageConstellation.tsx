"use client";

import { useEffect, useRef } from "react";

export function PageConstellation() {
	const canvasRef = useRef<HTMLCanvasElement>(null);

	useEffect(() => {
		const canvas = canvasRef.current;
		if (!canvas) return;
		const ctx = canvas.getContext("2d");
		if (!ctx) return;

		const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
		const dpr = Math.min(window.devicePixelRatio || 1, 2);

		type Dot = { x: number; y: number; vx: number; vy: number };
		let dots: Dot[] = [];
		let width = 0;
		let height = 0;
		const mouse = { x: -10000, y: -10000, active: false };

		const seed = () => {
			const target = Math.min(140, Math.max(40, Math.floor((width * height) / 18000)));
			dots = Array.from({ length: target }, () => ({
				x: Math.random() * width,
				y: Math.random() * height,
				vx: (Math.random() - 0.5) * 0.1,
				vy: (Math.random() - 0.5) * 0.1,
			}));
		};

		const resize = () => {
			width = window.innerWidth;
			height = window.innerHeight;
			canvas.width = Math.floor(width * dpr);
			canvas.height = Math.floor(height * dpr);
			canvas.style.width = `${width}px`;
			canvas.style.height = `${height}px`;
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			seed();
		};
		resize();

		const onMove = (e: MouseEvent) => {
			mouse.x = e.clientX;
			mouse.y = e.clientY;
			mouse.active = true;
		};
		const onLeave = () => {
			mouse.active = false;
			mouse.x = -10000;
			mouse.y = -10000;
		};

		window.addEventListener("resize", resize);
		window.addEventListener("mousemove", onMove);
		document.addEventListener("mouseleave", onLeave);
		window.addEventListener("blur", onLeave);

		const LINK_DIST = 105;
		const MOUSE_DIST = 165;

		const draw = () => {
			ctx.clearRect(0, 0, width, height);

			for (const d of dots) {
				d.x += d.vx;
				d.y += d.vy;
				if (d.x < 0 || d.x > width) d.vx *= -1;
				if (d.y < 0 || d.y > height) d.vy *= -1;
			}

			ctx.lineWidth = 0.5;
			for (let i = 0; i < dots.length; i++) {
				for (let j = i + 1; j < dots.length; j++) {
					const a = dots[i];
					const b = dots[j];
					const dx = a.x - b.x;
					const dy = a.y - b.y;
					const distSq = dx * dx + dy * dy;
					if (distSq < LINK_DIST * LINK_DIST) {
						const dist = Math.sqrt(distSq);
						const op = (1 - dist / LINK_DIST) * 0.05;
						ctx.strokeStyle = `rgba(240, 236, 232, ${op})`;
						ctx.beginPath();
						ctx.moveTo(a.x, a.y);
						ctx.lineTo(b.x, b.y);
						ctx.stroke();
					}
				}
			}

			if (mouse.active) {
				ctx.lineWidth = 0.6;
				for (const d of dots) {
					const dx = d.x - mouse.x;
					const dy = d.y - mouse.y;
					const distSq = dx * dx + dy * dy;
					if (distSq < MOUSE_DIST * MOUSE_DIST) {
						const dist = Math.sqrt(distSq);
						const op = (1 - dist / MOUSE_DIST) * 0.15;
						ctx.strokeStyle = `rgba(240, 236, 232, ${op})`;
						ctx.beginPath();
						ctx.moveTo(d.x, d.y);
						ctx.lineTo(mouse.x, mouse.y);
						ctx.stroke();
					}
				}
			}

			for (const d of dots) {
				let op = 0.11;
				if (mouse.active) {
					const dx = d.x - mouse.x;
					const dy = d.y - mouse.y;
					const distSq = dx * dx + dy * dy;
					if (distSq < MOUSE_DIST * MOUSE_DIST) {
						op += (1 - Math.sqrt(distSq) / MOUSE_DIST) * 0.22;
					}
				}
				ctx.fillStyle = `rgba(240, 236, 232, ${op})`;
				ctx.beginPath();
				ctx.arc(d.x, d.y, 1.0, 0, Math.PI * 2);
				ctx.fill();
			}
		};

		let raf = 0;
		const loop = () => {
			draw();
			raf = requestAnimationFrame(loop);
		};

		if (reducedMotion) draw();
		else loop();

		return () => {
			cancelAnimationFrame(raf);
			window.removeEventListener("resize", resize);
			window.removeEventListener("mousemove", onMove);
			document.removeEventListener("mouseleave", onLeave);
			window.removeEventListener("blur", onLeave);
		};
	}, []);

	return (
		<canvas ref={canvasRef} className="fixed inset-0 pointer-events-none" style={{ zIndex: 0 }} aria-hidden="true" />
	);
}
