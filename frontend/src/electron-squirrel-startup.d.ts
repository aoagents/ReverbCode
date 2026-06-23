// electron-squirrel-startup ships no types. Its default export is the boolean
// result of handling any --squirrel-* startup event (true when the app should
// quit, false on macOS/Linux or a normal launch).
declare module "electron-squirrel-startup" {
	const startup: boolean;
	export default startup;
}
