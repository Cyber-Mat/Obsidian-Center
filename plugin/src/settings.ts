import { App, PluginSettingTab, Setting, Notice } from "obsidian";
import type ObsidianCenterPlugin from "./main";

export interface ObsidianCenterSettings {
	serverUrl: string;
	vaultId: string;
	accessToken: string;
	refreshToken: string;
	debounceMs: number;
	autoConnect: boolean;
}

export const DEFAULT_SETTINGS: ObsidianCenterSettings = {
	serverUrl: "",
	vaultId: "",
	accessToken: "",
	refreshToken: "",
	debounceMs: 1000,
	autoConnect: true,
};

export class SettingsTab extends PluginSettingTab {
	plugin: ObsidianCenterPlugin;

	constructor(app: App, plugin: ObsidianCenterPlugin) {
		super(app, plugin);
		this.plugin = plugin;
	}

	display(): void {
		const { containerEl } = this;
		containerEl.empty();
		containerEl.createEl("h2", { text: "Obsidian Center" });

		// Server connection
		containerEl.createEl("h3", { text: "Server" });

		new Setting(containerEl)
			.setName("Server URL")
			.setDesc("The URL of your Obsidian Center server")
			.addText((text) =>
				text
					.setPlaceholder("https://your-server.example.com")
					.setValue(this.plugin.settings.serverUrl)
					.onChange(async (value) => {
						this.plugin.settings.serverUrl = value.trim();
						await this.plugin.saveSettings();
					})
			);

		// Login section
		containerEl.createEl("h3", { text: "Authentication" });

		if (this.plugin.settings.accessToken) {
			new Setting(containerEl)
				.setName("Status")
				.setDesc("Logged in")
				.addButton((btn) =>
					btn.setButtonText("Log out").onClick(async () => {
						this.plugin.settings.accessToken = "";
						this.plugin.settings.refreshToken = "";
						this.plugin.settings.vaultId = "";
						await this.plugin.saveSettings();
						this.plugin.disconnect();
						this.display();
					})
				);
		} else {
			let usernameInput = "";
			let passwordInput = "";

			new Setting(containerEl).setName("Username").addText((text) =>
				text.setPlaceholder("username").onChange((value) => {
					usernameInput = value;
				})
			);

			new Setting(containerEl).setName("Password").addText((text) =>
				text
					.setPlaceholder("password")
					.then((t) => (t.inputEl.type = "password"))
					.onChange((value) => {
						passwordInput = value;
					})
			);

			new Setting(containerEl)
				.addButton((btn) =>
					btn
						.setButtonText("Log in")
						.setCta()
						.onClick(async () => {
							await this.login(usernameInput, passwordInput);
						})
				)
				.addButton((btn) =>
					btn.setButtonText("Register").onClick(async () => {
						await this.register(usernameInput, passwordInput);
					})
				);
		}

		// Vault selection
		if (this.plugin.settings.accessToken) {
			containerEl.createEl("h3", { text: "Vault" });

			new Setting(containerEl)
				.setName("Vault ID")
				.setDesc("The vault to sync with (select or create below)")
				.addText((text) =>
					text
						.setValue(this.plugin.settings.vaultId)
						.setPlaceholder("vault-id")
						.onChange(async (value) => {
							this.plugin.settings.vaultId = value.trim();
							await this.plugin.saveSettings();
						})
				);

			new Setting(containerEl)
				.setName("Create new vault")
				.addButton((btn) =>
					btn.setButtonText("Create").onClick(async () => {
						await this.createVault();
					})
				);
		}

		// Sync settings
		containerEl.createEl("h3", { text: "Sync" });

		new Setting(containerEl)
			.setName("Debounce (ms)")
			.setDesc("Wait this long after a change before syncing")
			.addText((text) =>
				text
					.setValue(String(this.plugin.settings.debounceMs))
					.onChange(async (value) => {
						const n = parseInt(value, 10);
						if (!isNaN(n) && n >= 0) {
							this.plugin.settings.debounceMs = n;
							await this.plugin.saveSettings();
						}
					})
			);

		new Setting(containerEl)
			.setName("Auto-connect")
			.setDesc("Connect to server on startup")
			.addToggle((toggle) =>
				toggle
					.setValue(this.plugin.settings.autoConnect)
					.onChange(async (value) => {
						this.plugin.settings.autoConnect = value;
						await this.plugin.saveSettings();
					})
			);
	}

	private async login(username: string, password: string) {
		try {
			const resp = await fetch(
				`${this.plugin.settings.serverUrl}/api/auth/login`,
				{
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ username, password }),
				}
			);

			if (!resp.ok) {
				const msg = await safeErrorMessage(resp);
				new Notice(`Login failed: ${msg}`);
				return;
			}

			const data = await resp.json();
			this.plugin.settings.accessToken = data.access_token;
			this.plugin.settings.refreshToken = data.refresh_token;
			await this.plugin.saveSettings();
			new Notice("Logged in successfully");
			this.display();
		} catch (e) {
			new Notice(`Login failed: ${e}`);
		}
	}

	private async register(username: string, password: string) {
		try {
			const resp = await fetch(
				`${this.plugin.settings.serverUrl}/api/auth/register`,
				{
					method: "POST",
					headers: { "Content-Type": "application/json" },
					body: JSON.stringify({ username, password }),
				}
			);

			if (!resp.ok) {
				const msg = await safeErrorMessage(resp);
				new Notice(`Registration failed: ${msg}`);
				return;
			}

			const data = await resp.json();
			this.plugin.settings.accessToken = data.access_token;
			this.plugin.settings.refreshToken = data.refresh_token;
			await this.plugin.saveSettings();
			new Notice("Registered and logged in");
			this.display();
		} catch (e) {
			new Notice(`Registration failed: ${e}`);
		}
	}

	private async createVault() {
		const name = this.app.vault.getName();
		try {
			const resp = await fetch(
				`${this.plugin.settings.serverUrl}/api/vaults`,
				{
					method: "POST",
					headers: {
						"Content-Type": "application/json",
						Authorization: `Bearer ${this.plugin.settings.accessToken}`,
					},
					body: JSON.stringify({ name }),
				}
			);

			if (!resp.ok) {
				const msg = await safeErrorMessage(resp);
				new Notice(`Create vault failed: ${msg}`);
				return;
			}

			const vault = await resp.json();
			this.plugin.settings.vaultId = vault.id;
			await this.plugin.saveSettings();
			new Notice(`Vault "${name}" created`);
			this.display();
		} catch (e) {
			new Notice(`Create vault failed: ${e}`);
		}
	}
}

async function safeErrorMessage(resp: Response): Promise<string> {
	try {
		const body = await resp.json();
		return body.error || resp.statusText;
	} catch {
		return resp.statusText || `HTTP ${resp.status}`;
	}
}
