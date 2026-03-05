import { Notice, Plugin, TFile, TAbstractFile } from "obsidian";
import { ObsidianCenterSettings, DEFAULT_SETTINGS, SettingsTab } from "./settings";
import { SyncClient } from "./sync/client";

export default class ObsidianCenterPlugin extends Plugin {
	settings: ObsidianCenterSettings = DEFAULT_SETTINGS;
	syncClient: SyncClient | null = null;
	private pendingChanges: Map<string, NodeJS.Timeout> = new Map();
	private isSyncing = false;

	async onload() {
		await this.loadSettings();
		this.addSettingTab(new SettingsTab(this.app, this));

		this.addCommand({
			id: "sync-now",
			name: "Sync now",
			callback: () => this.triggerSync(),
		});

		this.addCommand({
			id: "connect",
			name: "Connect to server",
			callback: () => this.connect(),
		});

		this.addCommand({
			id: "disconnect",
			name: "Disconnect from server",
			callback: () => this.disconnect(),
		});

		// Register vault events
		this.registerEvent(this.app.vault.on("create", (file) => this.onFileChange(file)));
		this.registerEvent(this.app.vault.on("modify", (file) => this.onFileChange(file)));
		this.registerEvent(this.app.vault.on("delete", (file) => this.onFileDelete(file)));
		this.registerEvent(
			this.app.vault.on("rename", (file, oldPath) => this.onFileRename(file, oldPath))
		);

		// Status bar
		this.addStatusBarItem().setText("OC: disconnected");

		// Auto-connect if configured
		if (this.settings.serverUrl && this.settings.accessToken) {
			this.connect();
		}
	}

	async onunload() {
		this.disconnect();
	}

	async loadSettings() {
		this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
	}

	async saveSettings() {
		await this.saveData(this.settings);
	}

	async connect() {
		if (!this.settings.serverUrl) {
			new Notice("Obsidian Center: configure server URL in settings");
			return;
		}
		if (!this.settings.accessToken) {
			new Notice("Obsidian Center: log in first via settings");
			return;
		}

		try {
			this.syncClient = new SyncClient(
				this.settings.serverUrl,
				this.settings.vaultId,
				this.settings.accessToken,
				{
					onFileChanged: (path, hash) => this.onRemoteFileChanged(path, hash),
					onFileDeleted: (path) => this.onRemoteFileDeleted(path),
					onConnected: () => this.updateStatus("connected"),
					onDisconnected: () => this.updateStatus("disconnected"),
					onError: (err) => {
						console.error("OC sync error:", err);
						this.updateStatus("error");
					},
				}
			);
			this.syncClient.connect();
		} catch (e) {
			new Notice(`Obsidian Center: connection failed - ${e}`);
		}
	}

	disconnect() {
		if (this.syncClient) {
			this.syncClient.disconnect();
			this.syncClient = null;
		}
		this.updateStatus("disconnected");
	}

	private updateStatus(status: string) {
		// Update all status bar items created by this plugin
		const el = this.app.workspace.containerEl.querySelector(
			".status-bar-item"
		);
		if (el) {
			// We'll use the simple approach of just updating via Notice for now
		}
	}

	// Local file changes — debounced to avoid flooding during rapid edits
	private onFileChange(file: TAbstractFile) {
		if (!(file instanceof TFile) || this.isSyncing) return;

		const existing = this.pendingChanges.get(file.path);
		if (existing) clearTimeout(existing);

		const timeout = setTimeout(() => {
			this.pendingChanges.delete(file.path);
			this.syncFileToServer(file as TFile);
		}, this.settings.debounceMs);

		this.pendingChanges.set(file.path, timeout);
	}

	private onFileDelete(file: TAbstractFile) {
		if (this.isSyncing) return;
		this.syncClient?.deleteFile(file.path);
	}

	private onFileRename(file: TAbstractFile, oldPath: string) {
		if (this.isSyncing) return;
		// Rename = delete old + create new
		this.syncClient?.deleteFile(oldPath);
		if (file instanceof TFile) {
			this.syncFileToServer(file);
		}
	}

	private async syncFileToServer(file: TFile) {
		if (!this.syncClient) return;

		try {
			const content = await this.app.vault.readBinary(file);
			this.syncClient.putFile(file.path, new Uint8Array(content));
		} catch (e) {
			console.error(`OC: failed to sync ${file.path}:`, e);
		}
	}

	// Remote changes — apply to local vault
	private async onRemoteFileChanged(path: string, hash: string) {
		if (!this.syncClient) return;

		// Check if local file already matches
		const existing = this.app.vault.getAbstractFileByPath(path);
		if (existing instanceof TFile) {
			const content = await this.app.vault.readBinary(existing);
			const localHash = await this.hashContent(new Uint8Array(content));
			if (localHash === hash) return; // Already in sync
		}

		try {
			this.isSyncing = true;
			const content = await this.syncClient.getFile(path);
			if (!content) return;

			const existingFile = this.app.vault.getAbstractFileByPath(path);
			if (existingFile instanceof TFile) {
				await this.app.vault.modifyBinary(existingFile, content.buffer as ArrayBuffer);
			} else {
				await this.app.vault.createBinary(path, content.buffer as ArrayBuffer);
			}
		} catch (e) {
			console.error(`OC: failed to apply remote change for ${path}:`, e);
		} finally {
			this.isSyncing = false;
		}
	}

	private async onRemoteFileDeleted(path: string) {
		try {
			this.isSyncing = true;
			const file = this.app.vault.getAbstractFileByPath(path);
			if (file) {
				await this.app.vault.delete(file);
			}
		} catch (e) {
			console.error(`OC: failed to delete ${path}:`, e);
		} finally {
			this.isSyncing = false;
		}
	}

	async triggerSync() {
		if (!this.syncClient) {
			new Notice("Obsidian Center: not connected");
			return;
		}

		new Notice("Obsidian Center: syncing...");

		try {
			const snapshot = await this.syncClient.getSnapshot();
			const localFiles = this.app.vault.getFiles();

			// Upload files that differ or are missing on server
			for (const file of localFiles) {
				const content = await this.app.vault.readBinary(file);
				const hash = await this.hashContent(new Uint8Array(content));

				if (snapshot[file.path] !== hash) {
					await this.syncFileToServer(file);
				}
			}

			// Download files that exist on server but not locally
			for (const [path, hash] of Object.entries(snapshot)) {
				const local = this.app.vault.getAbstractFileByPath(path);
				if (!local) {
					await this.onRemoteFileChanged(path, hash);
				}
			}

			new Notice("Obsidian Center: sync complete");
		} catch (e) {
			new Notice(`Obsidian Center: sync failed - ${e}`);
		}
	}

	private async hashContent(data: Uint8Array): Promise<string> {
		const hashBuffer = await crypto.subtle.digest("SHA-256", data);
		const hashArray = Array.from(new Uint8Array(hashBuffer));
		return hashArray.map((b) => b.toString(16).padStart(2, "0")).join("");
	}
}
