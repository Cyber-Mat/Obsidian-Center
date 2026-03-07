import * as Automerge from "@automerge/automerge";
import type { Doc, SyncState, Patch } from "@automerge/automerge";

export interface VaultDocType {
  [key: string]: unknown;
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
 * CRDTManager wraps an Automerge document for the web editor.
 * Same interface as the plugin's CRDTManager.
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

  load(data: Uint8Array): void {
    this.doc = Automerge.load<VaultDocType>(data);
    this.syncState = Automerge.initSyncState();
  }

  save(): Uint8Array {
    return Automerge.save(this.doc);
  }

  getFileContent(path: string): string | null {
    const file = this.doc.files?.[path];
    if (!file) return null;
    return file.content;
  }

  getFileInfo(path: string): { hash: string; modified: number; is_binary: boolean } | null {
    const file = this.doc.files?.[path];
    if (!file) return null;
    return { hash: file.hash, modified: file.modified, is_binary: file.is_binary };
  }

  getAllFiles(): string[] {
    return Object.keys(this.doc.files || {});
  }

  updateFileContent(path: string, content: string): void {
    const hash = this.contentHash(content);

    // Ensure file entry exists and update metadata
    this.doc = Automerge.change(this.doc, `update ${path}`, (d) => {
      if (!d.files[path]) {
        d.files[path] = {
          content: "",
          hash: "",
          modified: 0,
          is_binary: false,
        };
      }
      d.files[path].modified = Date.now();
      d.files[path].is_binary = false;
      d.files[path].hash = hash;
    });

    // Use updateText for character-level CRDT merging — must capture returned doc
    this.doc = Automerge.updateText(this.doc, ["files", path, "content"], content);
  }

  deleteFile(path: string): void {
    this.doc = Automerge.change(this.doc, `delete ${path}`, (d) => {
      delete d.files[path];
    });
  }

  createFile(path: string, content: string): void {
    this.doc = Automerge.change(this.doc, `create ${path}`, (d) => {
      d.files[path] = {
        content: "",
        hash: "",
        modified: Date.now(),
        is_binary: false,
      };
    });
    this.doc = Automerge.updateText(this.doc, ["files", path, "content"], content);
  }

  generateSyncMessage(): Uint8Array | null {
    const [newSyncState, msg] = Automerge.generateSyncMessage(this.doc, this.syncState);
    this.syncState = newSyncState;
    return msg;
  }

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

  onPatch(callback: (patches: Patch[]) => void): void {
    this.patchCallbacks.push(callback);
  }

  resetSyncState(): void {
    this.syncState = Automerge.initSyncState();
  }

  private contentHash(data: string): string {
    let h = 0x811c9dc5;
    for (let i = 0; i < data.length; i++) {
      h ^= data.charCodeAt(i);
      h = Math.imul(h, 0x01000193);
    }
    const prefix = (h >>> 0).toString(16).padStart(8, "0");
    return `fnv:${prefix}:${data.length}`;
  }
}
