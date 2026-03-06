import { Notice, Plugin, TFile, TAbstractFile } from "obsidian";
import { ObsidianCenterSettings, DEFAULT_SETTINGS, SettingsTab } from "./settings";
import { SyncClient } from "./sync/client";
import { CRDTManager } from "./sync/crdt";
import type { Patch } from "@automerge/automerge";

const CRDT_SAVE_KEY = "crdt-state";

export default class ObsidianCenterPlugin extends Plugin {
	settings: ObsidianCenterSettings = DEFAULT_SETTINGS;
	syncClient: SyncClient | null = null;
	crdt: CRDTManager = new CRDTManager();
	private pendingChanges: Map<string, ReturnType<typeof setTimeout>> = new Map();
	private isApplyingRemote = false;
	private saveTimer: ReturnType<typeof setTimeout> | null = null;
	private statusBarEl: HTMLElement | null = null;

	async onload() {
		await this.loadSettings();
		await this.loadCRDTState();
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
		this.statusBarEl = this.addStatusBarItem();
		this.updateStatus("disconnected");

		// Auto-connect if configured
		if (this.settings.serverUrl && this.settings.accessToken) {
			this.connect();
		}
	}

	async onunload() {
		this.disconnect();
		await this.saveCRDTState();
	}

	async loadSettings() {
		this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
	}

	async saveSettings() {
		await this.saveData(this.settings);
	}

	private async loadCRDTState() {
		try {
			const adapter = this.app.vault.adapter;
			const crdtPath = `${this.manifest.dir}/${CRDT_SAVE_KEY}.bin`;
			if (await adapter.exists(crdtPath)) {
				const data = await adapter.readBinary(crdtPath);
				this.crdt.load(new Uint8Array(data));
				console.log("OC: loaded CRDT state from disk");
			}
		} catch (e) {
			console.warn("OC: failed to load CRDT state, starting fresh:", e);
			this.crdt = new CRDTManager();
		}
	}

	private async saveCRDTState() {
		try {
			const data = this.crdt.save();
			const adapter = this.app.vault.adapter;
			const crdtPath = `${this.manifest.dir}/${CRDT_SAVE_KEY}.bin`;
			await adapter.writeBinary(crdtPath, toArrayBuffer(data));
		} catch (e) {
			console.error("OC: failed to save CRDT state:", e);
		}
	}

	private scheduleCRDTSave() {
		if (this.saveTimer) clearTimeout(this.saveTimer);
		this.saveTimer = setTimeout(() => {
			this.saveCRDTState();
		}, 5000);
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
				this.crdt,
				{
					onPatchesApplied: (patches) => this.onRemotePatches(patches),
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
		if (!this.statusBarEl) return;
		const labels: Record<string, string> = {
			connected: "OC: connected",
			disconnected: "OC: disconnected",
			error: "OC: error",
			syncing: "OC: syncing...",
		};
		this.statusBarEl.setText(labels[status] || `OC: ${status}`);
	}

	// --- Local file changes → CRDT → sync ---

	private onFileChange(file: TAbstractFile) {
		if (!(file instanceof TFile) || this.isApplyingRemote) return;

		const existing = this.pendingChanges.get(file.path);
		if (existing) clearTimeout(existing);

		const timeout = setTimeout(() => {
			this.pendingChanges.delete(file.path);
			this.syncFileLocally(file as TFile);
		}, this.settings.debounceMs);

		this.pendingChanges.set(file.path, timeout);
	}

	private onFileDelete(file: TAbstractFile) {
		if (this.isApplyingRemote) return;

		this.crdt.deleteFile(file.path);
		this.syncClient?.pushChanges();
		this.scheduleCRDTSave();
	}

	private onFileRename(file: TAbstractFile, oldPath: string) {
		if (this.isApplyingRemote) return;

		// Rename = delete old + create new
		this.crdt.deleteFile(oldPath);
		if (file instanceof TFile) {
			this.syncFileLocally(file);
		} else {
			this.syncClient?.pushChanges();
		}
		this.scheduleCRDTSave();
	}

	private async syncFileLocally(file: TFile) {
		try {
			const content = await this.app.vault.readBinary(file);
			const data = new Uint8Array(content);
			const isBinary = this.isBinaryFile(file.path, data);

			this.crdt.putFile(file.path, data, isBinary);
			this.syncClient?.pushChanges();
			this.scheduleCRDTSave();
		} catch (e) {
			console.error(`OC: failed to sync ${file.path} locally:`, e);
		}
	}

	// --- Remote patches → local vault ---

	private async onRemotePatches(patches: Patch[]) {
		// Determine which file paths were affected
		const affectedPaths = new Set<string>();
		const deletedPaths = new Set<string>();

		for (const patch of patches) {
			if (patch.path.length >= 2 && patch.path[0] === "files") {
				const filePath = patch.path[1] as string;
				if (patch.action === "del" && patch.path.length === 2) {
					deletedPaths.add(filePath);
				} else {
					affectedPaths.add(filePath);
				}
			}
		}

		this.isApplyingRemote = true;
		try {
			// Handle deletions
			for (const path of deletedPaths) {
				const file = this.app.vault.getAbstractFileByPath(path);
				if (file) {
					await this.app.vault.delete(file);
				}
			}

			// Handle creates/updates
			for (const path of affectedPaths) {
				if (deletedPaths.has(path)) continue;

				const content = this.crdt.getFileContent(path);
				if (!content) continue;

				const buf = toArrayBuffer(content);
				const existingFile = this.app.vault.getAbstractFileByPath(path);
				if (existingFile instanceof TFile) {
					// Check if content actually changed
					const localContent = await this.app.vault.readBinary(existingFile);
					if (!arraysEqual(new Uint8Array(localContent), content)) {
						await this.app.vault.modifyBinary(existingFile, buf);
					}
				} else {
					// Ensure parent directories exist
					const dir = path.substring(0, path.lastIndexOf("/"));
					if (dir && !this.app.vault.getAbstractFileByPath(dir)) {
						await this.app.vault.createFolder(dir);
					}
					await this.app.vault.createBinary(path, buf);
				}
			}
		} catch (e) {
			console.error("OC: failed to apply remote patches:", e);
		} finally {
			this.isApplyingRemote = false;
		}

		this.scheduleCRDTSave();
	}

	// --- Manual sync command ---

	async triggerSync() {
		if (!this.syncClient) {
			new Notice("Obsidian Center: not connected");
			return;
		}

		new Notice("Obsidian Center: syncing...");

		try {
			// Push all local files into CRDT
			const localFiles = this.app.vault.getFiles();
			for (const file of localFiles) {
				const content = await this.app.vault.readBinary(file);
				const data = new Uint8Array(content);
				const isBinary = this.isBinaryFile(file.path, data);

				// Only update if not already tracked
				const existingHash = this.crdt.getFileHash(file.path);
				if (!existingHash) {
					this.crdt.putFile(file.path, data, isBinary);
				}
			}

			// Push changes to server
			this.syncClient.pushChanges();
			this.scheduleCRDTSave();

			new Notice("Obsidian Center: sync initiated");
		} catch (e) {
			new Notice(`Obsidian Center: sync failed - ${e}`);
		}
	}

	private isBinaryFile(path: string, content: Uint8Array): boolean {
		const binaryExts = [
			".png", ".jpg", ".jpeg", ".gif", ".webp", ".pdf",
			".zip", ".tar", ".gz", ".mp3", ".mp4", ".wav", ".ogg",
		];
		const lower = path.toLowerCase();
		for (const ext of binaryExts) {
			if (lower.endsWith(ext)) return true;
		}
		const check = content.subarray(0, Math.min(content.length, 512));
		for (let i = 0; i < check.length; i++) {
			if (check[i] === 0) return true;
		}
		return false;
	}
}

/**
 * Safely convert Uint8Array to ArrayBuffer, handling cases where the
 * typed array's buffer may be larger than its view (e.g. shared buffers).
 */
function toArrayBuffer(data: Uint8Array): ArrayBuffer {
	return data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength) as ArrayBuffer;
}

function arraysEqual(a: Uint8Array, b: Uint8Array): boolean {
	if (a.length !== b.length) return false;
	for (let i = 0; i < a.length; i++) {
		if (a[i] !== b[i]) return false;
	}
	return true;
}
