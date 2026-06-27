import { createMDX } from "fumadocs-mdx/next";

/** @type {import('next').NextConfig} */
const nextConfig = {
	devIndicators: false,
	outputFileTracingRoot: new URL("./", import.meta.url).pathname,
};

const withMDX = createMDX();

export default withMDX(nextConfig);
