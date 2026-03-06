import type { Patch } from "@automerge/automerge";
import { CRDTManager } from "./crdt";

export interface SyncCallbacks {
  onPatchesApplied: (patches: Patch[]) => void;
  onConnected: () => void;
  onDisconnected: () => void;
  onError: (error: Error) => void;
}

interface WireMessage {
  type: string;
  data?: unknown;
}

interface SyncData {
  message: string;
}

const MAX_RECONNECT_ATTEMPTS = 20;

export class SyncClient {
  private ws: WebSocket | null = null;
  private serverUrl: string;
  private vaultId: string;
  private token: string;
  private callbacks: SyncCallbacks;
  private crdt: CRDTManager;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private reconnectDelay = 1000;
  private maxReconnectDelay = 30000;
  private shouldReconnect = true;
  private syncInFlight = false;
  private reconnectAttempts = 0;

  constructor(
    serverUrl: string,
    vaultId: string,
    token: string,
    crdt: CRDTManager,
    callbacks: SyncCallbacks
  ) {
    this.serverUrl = serverUrl;
    this.vaultId = vaultId;
    this.token = token;
    this.crdt = crdt;
    this.callbacks = callbacks;
  }

  connect(): void {
    this.shouldReconnect = true;
    this.reconnectAttempts = 0;
    this.doConnect();
  }

  disconnect(): void {
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

  pushChanges(): void {
    this.sendPendingSyncMessages();
  }

  updateToken(token: string): void {
    this.token = token;
  }

  isConnected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }

  private doConnect(): void {
    const wsUrl = this.serverUrl.replace(/^http/, "ws").replace(/\/$/, "");
    this.crdt.resetSyncState();

    // Connect without token in URL — send auth as first message
    this.ws = new WebSocket(`${wsUrl}/api/sync/${this.vaultId}`);

    this.ws.onopen = () => {
      this.reconnectDelay = 1000;
      this.reconnectAttempts = 0;

      // Send auth as first message
      const authMsg: WireMessage = {
        type: "auth",
        data: { token: this.token },
      };
      this.ws!.send(JSON.stringify(authMsg));
      this.callbacks.onConnected();
      this.sendPendingSyncMessages();
    };

    this.ws.onmessage = (event: MessageEvent) => {
      try {
        const msg: WireMessage = JSON.parse(event.data as string);
        this.handleMessage(msg);
      } catch (e) {
        console.error("OC web: failed to parse message:", e);
      }
    };

    this.ws.onclose = () => {
      this.callbacks.onDisconnected();
      this.scheduleReconnect();
    };

    this.ws.onerror = () => {
      this.callbacks.onError(new Error("WebSocket error"));
    };
  }

  private scheduleReconnect(): void {
    if (!this.shouldReconnect) return;

    this.reconnectAttempts++;
    if (this.reconnectAttempts > MAX_RECONNECT_ATTEMPTS) {
      this.callbacks.onError(
        new Error(`Reconnection failed after ${MAX_RECONNECT_ATTEMPTS} attempts`)
      );
      return;
    }

    this.reconnectTimer = setTimeout(() => {
      this.doConnect();
    }, this.reconnectDelay);

    this.reconnectDelay = Math.min(this.reconnectDelay * 2, this.maxReconnectDelay);
  }

  private handleMessage(msg: WireMessage): void {
    if (!msg || typeof msg.type !== "string") return;

    switch (msg.type) {
      case "sync": {
        const data = msg.data as SyncData;
        if (!data?.message) return;
        const messageBytes = base64ToUint8Array(data.message);
        const patches = this.crdt.receiveSyncMessage(messageBytes);
        if (patches.length > 0) {
          this.callbacks.onPatchesApplied(patches);
        }
        this.sendPendingSyncMessages();
        break;
      }
      case "pong":
        break;
      case "error": {
        const data = msg.data as { message: string };
        this.callbacks.onError(new Error(data?.message || "unknown error"));
        break;
      }
    }
  }

  private sendPendingSyncMessages(): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    if (this.syncInFlight) return;

    this.syncInFlight = true;
    try {
      let msg = this.crdt.generateSyncMessage();
      while (msg !== null) {
        const wireMsg: WireMessage = {
          type: "sync",
          data: { message: uint8ArrayToBase64(msg) },
        };
        this.ws.send(JSON.stringify(wireMsg));
        msg = this.crdt.generateSyncMessage();
      }
    } finally {
      this.syncInFlight = false;
    }
  }
}

function uint8ArrayToBase64(bytes: Uint8Array): string {
  const chunkSize = 8192;
  let binary = "";
  for (let i = 0; i < bytes.length; i += chunkSize) {
    const chunk = bytes.subarray(i, Math.min(i + chunkSize, bytes.length));
    binary += String.fromCharCode(...chunk);
  }
  return btoa(binary);
}

function base64ToUint8Array(base64: string): Uint8Array {
  const binary = atob(base64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}
