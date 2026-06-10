import { app, BrowserWindow } from "electron";

function createWindow(): void {
	const window = new BrowserWindow({
		width: 1200,
		height: 800,
		webPreferences: {
			contextIsolation: true,
			nodeIntegration: false,
		},
	});

	void window.loadURL("about:blank");
}

app.whenReady().then(() => {
	createWindow();

	app.on("activate", () => {
		if (BrowserWindow.getAllWindows().length === 0) {
			createWindow();
		}
	});
});

app.on("window-all-closed", () => {
	if (process.platform !== "darwin") {
		app.quit();
	}
});
