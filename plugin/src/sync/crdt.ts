import * as Automerge from "@automerge/automerge";
import type { Doc, SyncState, Patch } from "@automerge/automerge";

/**
 * Shape of the CRDT vault document.
 */
export interface VaultDocType {
	files: Record<
		string,
		{
			content: string;
			hash: string;
			modified: number;
			is_binary: boolean;
		}
	>;
	metadata: {
		schema_version: number;
	};
}

/**
 * CRDTManager wraps an Automerge document representing the vault state.
 *
 * All local mutations go through this manager, which keeps the doc
 * in sync and allows generating/receiving sync messages for the server.
 */
export class CRDTManager {
	private doc: Doc<VaultDocType>;
	private syncState: SyncState;
	private patchCallbacks: Array<(patches: Patch[]) => void> = [];

	constructor() {
		this.doc = Automerge.from<VaultDocType>({
			files: {},
			metadata: { schema_version: 1 },
		});
		this.syncState = Automerge.initSyncState();
	}

	/**
	 * Load a previously saved CRDT document from binary.
	 */
	load(data: Uint8Array): void {
		this.doc = Automerge.load<VaultDocType>(data);
		this.syncState = Automerge.initSyncState();
	}

	/**
	 * Serialize the document to binary for persistence.
	 */
	save(): Uint8Array {
		return Automerge.save(this.doc);
	}

	/**
	 * Update or create a file in the CRDT document.
	 */
	putFile(path: string, content: Uint8Array, isBinary: boolean): void {
		const hash = this.sha256Hex(content);
		const textContent = isBinary
			? btoa(String.fromCharCode(...content))
			: new TextDecoder().decode(content);

		this.doc = Automerge.change(this.doc, `update ${path}`, (d) => {
			if (!d.files[path]) {
				d.files[path] = {
					content: "",
					hash: "",
					modified: 0,
					is_binary: false,
				};
			}
			d.files[path].hash = hash;
			d.files[path].modified = Date.now();
			d.files[path].is_binary = isBinary;
		});

		// Use updateText for text diffs (better CRDT merging)
		if (!isBinary) {
			Automerge.updateText(this.doc, ["files", path, "content"], textContent);
		} else {
			this.doc = Automerge.change(this.doc, (d) => {
				d.files[path].content = textContent;
			});
		}
	}

	/**
	 * Delete a file from the CRDT document.
	 */
	deleteFile(path: string): void {
		this.doc = Automerge.change(this.doc, `delete ${path}`, (d) => {
			delete d.files[path];
		});
	}

	/**
	 * Read file content from the CRDT document.
	 */
	getFileContent(path: string): Uint8Array | null {
		const file = this.doc.files?.[path];
		if (!file) return null;

		if (file.is_binary) {
			const binaryString = atob(file.content);
			const bytes = new Uint8Array(binaryString.length);
			for (let i = 0; i < binaryString.length; i++) {
				bytes[i] = binaryString.charCodeAt(i);
			}
			return bytes;
		}

		return new TextEncoder().encode(file.content);
	}

	/**
	 * Get file hash from the CRDT document.
	 */
	getFileHash(path: string): string | null {
		return this.doc.files?.[path]?.hash ?? null;
	}

	/**
	 * List all file paths in the document.
	 */
	getAllFiles(): string[] {
		return Object.keys(this.doc.files || {});
	}

	/**
	 * Get a snapshot (path → hash) of all files.
	 */
	getSnapshot(): Record<string, string> {
		const snapshot: Record<string, string> = {};
		for (const [path, file] of Object.entries(this.doc.files || {})) {
			snapshot[path] = file.hash;
		}
		return snapshot;
	}

	/**
	 * Generate the next sync message to send to the server.
	 * Returns null if already in sync.
	 */
	generateSyncMessage(): Uint8Array | null {
		const [newSyncState, msg] = Automerge.generateSyncMessage(
			this.doc,
			this.syncState
		);
		this.syncState = newSyncState;
		return msg;
	}

	/**
	 * Receive a sync message from the server.
	 * Returns patches describing what changed.
	 */
	receiveSyncMessage(message: Uint8Array): Patch[] {
		const patches: Patch[] = [];
		const [newDoc, newSyncState] = Automerge.receiveSyncMessage(
			this.doc,
			this.syncState,
			message,
			{
				patchCallback: (p) => {
					patches.push(...p);
				},
			}
		);
		this.doc = newDoc;
		this.syncState = newSyncState;

		if (patches.length > 0) {
			for (const cb of this.patchCallbacks) {
				cb(patches);
			}
		}

		return patches;
	}

	/**
	 * Register a callback for remote patches.
	 */
	onPatch(callback: (patches: Patch[]) => void): void {
		this.patchCallbacks.push(callback);
	}

	/**
	 * Reset sync state (e.g. on reconnect to a fresh server).
	 */
	resetSyncState(): void {
		this.syncState = Automerge.initSyncState();
	}

	/**
	 * Compute SHA-256 hex string synchronously using SubtleCrypto isn't
	 * available synchronously, so we use a simple hash for the CRDT metadata.
	 * The server computes the real SHA-256; this is just for quick comparison.
	 */
	private sha256Hex(data: Uint8Array): string {
		// Simple FNV-1a based hash for local comparison.
		// The authoritative hash is computed server-side.
		let h = 0x811c9dc5;
		for (let i = 0; i < data.length; i++) {
			h ^= data[i];
			h = Math.imul(h, 0x01000193);
		}
		// Also include length to reduce collisions
		const prefix = (h >>> 0).toString(16).padStart(8, "0");
		return `fnv:${prefix}:${data.length}`;
	}
}
