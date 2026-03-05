export interface SyncCallbacks {
	onFileChanged: (path: string, hash: string) => void;
	onFileDeleted: (path: string) => void;
	onConnected: () => void;
	onDisconnected: () => void;
	onError: (error: Error) => void;
}

interface SyncMessage {
	type: string;
	data?: unknown;
}

export class SyncClient {
	private ws: WebSocket | null = null;
	private serverUrl: string;
	private vaultId: string;
	private token: string;
	private callbacks: SyncCallbacks;
	private reconnectTimer: NodeJS.Timeout | null = null;
	private reconnectDelay = 1000;
	private maxReconnectDelay = 30000;
	private shouldReconnect = true;

	constructor(
		serverUrl: string,
		vaultId: string,
		token: string,
		callbacks: SyncCallbacks
	) {
		this.serverUrl = serverUrl;
		this.vaultId = vaultId;
		this.token = token;
		this.callbacks = callbacks;
	}

	connect() {
		this.shouldReconnect = true;
		this.doConnect();
	}

	disconnect() {
		this.shouldReconnect = false;
		if (this.reconnectTimer) {
			clearTimeout(this.reconnectTimer);
			this.reconnectTimer = null;
		}
		if (this.ws) {
			this.ws.close(1000, "client disconnect");
			this.ws = null;
		}
	}

	private doConnect() {
		const wsUrl = this.serverUrl
			.replace(/^http/, "ws")
			.replace(/\/$/, "");

		this.ws = new WebSocket(
			`${wsUrl}/api/sync/${this.vaultId}?token=${encodeURIComponent(this.token)}`
		);

		this.ws.onopen = () => {
			this.reconnectDelay = 1000;
			this.callbacks.onConnected();
		};

		this.ws.onmessage = (event) => {
			try {
				const msg: SyncMessage = JSON.parse(event.data);
				this.handleMessage(msg);
			} catch (e) {
				console.error("OC: failed to parse message:", e);
			}
		};

		this.ws.onclose = () => {
			this.callbacks.onDisconnected();
			this.scheduleReconnect();
		};

		this.ws.onerror = (event) => {
			this.callbacks.onError(new Error("WebSocket error"));
		};
	}

	private scheduleReconnect() {
		if (!this.shouldReconnect) return;

		this.reconnectTimer = setTimeout(() => {
			this.doConnect();
		}, this.reconnectDelay);

		this.reconnectDelay = Math.min(
			this.reconnectDelay * 2,
			this.maxReconnectDelay
		);
	}

	private handleMessage(msg: SyncMessage) {
		switch (msg.type) {
			case "snapshot":
				// Initial snapshot received on connect — handled by full sync
				break;
			case "file_changed": {
				const data = msg.data as { path: string; hash: string };
				this.callbacks.onFileChanged(data.path, data.hash);
				break;
			}
			case "file_deleted": {
				const data = msg.data as { path: string };
				this.callbacks.onFileDeleted(data.path);
				break;
			}
			case "pong":
				break;
			case "error": {
				const data = msg.data as { message: string };
				this.callbacks.onError(new Error(data.message));
				break;
			}
		}
	}

	putFile(path: string, content: Uint8Array) {
		this.send({
			type: "put_file",
			data: {
				path,
				content: Array.from(content),
			},
		});
	}

	deleteFile(path: string) {
		this.send({
			type: "delete_file",
			data: { path },
		});
	}

	private send(msg: SyncMessage) {
		if (this.ws && this.ws.readyState === WebSocket.OPEN) {
			this.ws.send(JSON.stringify(msg));
		}
	}

	// REST API methods for operations that don't fit WebSocket well

	async getSnapshot(): Promise<Record<string, string>> {
		const resp = await fetch(
			`${this.serverUrl}/api/vaults/${this.vaultId}/snapshot`,
			{
				headers: {
					Authorization: `Bearer ${this.token}`,
				},
			}
		);
		if (!resp.ok) throw new Error(`snapshot failed: ${resp.status}`);
		const data = await resp.json();
		return data.files || {};
	}

	async getFile(path: string): Promise<Uint8Array | null> {
		const resp = await fetch(
			`${this.serverUrl}/api/vaults/${this.vaultId}/files/${encodeURIComponent(path)}`,
			{
				headers: {
					Authorization: `Bearer ${this.token}`,
				},
			}
		);
		if (!resp.ok) return null;
		const buf = await resp.arrayBuffer();
		return new Uint8Array(buf);
	}
}
