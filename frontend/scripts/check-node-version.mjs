const supportedMajor = 20;
const currentMajor = Number.parseInt(process.versions.node.split(".")[0], 10);

if (currentMajor !== supportedMajor) {
	console.error(
		[
			`Unsupported Node.js runtime: ${process.versions.node}.`,
			`The Electron frontend is currently supported on Node ${supportedMajor}.x only.`,
			"",
			"Why this is enforced:",
			"- CI and desktop packaging run on Node 20.",
			"- Newer Node 24/26 releases currently hit upstream Electron/Forge install bugs",
			"  that can leave node_modules/electron partially extracted and make",
			"  `npm run dev` fail with `Electron failed to install correctly`.",
			"",
			"Switch to Node 20 and reinstall dependencies:",
			"  nvm use 20 || nvm install 20",
			"  rm -rf node_modules",
			"  npm ci",
		].join("\n"),
	);
	process.exit(1);
}
