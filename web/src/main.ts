/**
 * Obsidian Center Web Editor — main entry point.
 *
 * Wires together: auth UI, file tree, CodeMirror editor, and Automerge CRDT sync.
 */

import * as api from "./api";
import { CRDTManager } from "./sync/crdt";
import { SyncClient } from "./sync/client";
import { FileTree } from "./filetree";
import { Editor } from "./editor";
import type { Patch } from "@automerge/automerge";

// ── State ──

let crdt = new CRDTManager();
let syncClient: SyncClient | null = null;
let fileTree: FileTree | null = null;
let editor: Editor | null = null;
let currentVaultId = "";

// ── DOM elements ──

const authScreen = document.getElementById("auth-screen")!;
const editorScreen = document.getElementById("editor-screen")!;
const authForm = document.getElementById("auth-form") as HTMLFormElement;
const authUsername = document.getElementById("auth-username") as HTMLInputElement;
const authPassword = document.getElementById("auth-password") as HTMLInputElement;
const authError = document.getElementById("auth-error")!;
const btnLogin = document.getElementById("btn-login")!;
const btnRegister = document.getElementById("btn-register")!;
const btnLogout = document.getElementById("btn-logout")!;
const syncStatus = document.getElementById("sync-status")!;
const currentFileEl = document.getElementById("current-file")!;
const vaultSelect = document.getElementById("vault-select") as HTMLSelectElement;
const btnNewFile = document.getElementById("btn-new-file")!;
const fileTreeEl = document.getElementById("file-tree")!;
const editorEl = document.getElementById("editor")!;
const emptyStateEl = document.getElementById("empty-state")!;

// ── Initialization ──

function init() {
  // Determine server URL — same origin as the web editor
  api.setBaseUrl(window.location.origin);

  // Bind auth events
  authForm.addEventListener("submit", (e) => {
    e.preventDefault();
    doLogin();
  });
  btnRegister.addEventListener("click", doRegister);
  btnLogout.addEventListener("click", doLogout);
  vaultSelect.addEventListener("change", onVaultChange);
  btnNewFile.addEventListener("click", onNewFile);

  // Persist CRDT state to sessionStorage on unload
  window.addEventListener("beforeunload", () => {
    saveCRDTState();
  });

  // Check for existing session
  if (api.loadTokens()) {
    showEditor();
  }
}

// ── Auth ──

async function doLogin() {
  authError.hidden = true;
  try {
    await api.login(authUsername.value, authPassword.value);
    showEditor();
  } catch (e) {
    authError.textContent = String(e instanceof Error ? e.message : e);
    authError.hidden = false;
  }
}

async function doRegister() {
  authError.hidden = true;
  try {
    await api.register(authUsername.value, authPassword.value);
    showEditor();
  } catch (e) {
    authError.textContent = String(e instanceof Error ? e.message : e);
    authError.hidden = false;
  }
}

function doLogout() {
  disconnectSync();
  // Clear vault-specific CRDT state
  if (currentVaultId) {
    sessionStorage.removeItem(`oc_crdt_${currentVaultId}`);
  }
  api.clearTokens();
  sessionStorage.removeItem("oc_vault_id");
  currentVaultId = "";
  authScreen.hidden = false;
  editorScreen.hidden = true;
  authUsername.value = "";
  authPassword.value = "";
}

// ── Editor screen ──

async function showEditor() {
  authScreen.hidden = true;
  editorScreen.hidden = false;

  // Set up file tree
  fileTree = new FileTree(fileTreeEl, {
    onFileSelect: (path) => openFile(path),
  });

  // Set up editor
  editor = new Editor(editorEl, emptyStateEl, crdt, {
    onContentChange: (path, content) => {
      syncClient?.pushChanges();
      scheduleCRDTSave();
    },
  });

  // Load vaults
  try {
    const vaults = await api.listVaults();
    vaultSelect.innerHTML = "";

    if (vaults.length === 0) {
      const opt = document.createElement("option");
      opt.textContent = "No vaults";
      opt.disabled = true;
      vaultSelect.appendChild(opt);
      return;
    }

    for (const v of vaults) {
      const opt = document.createElement("option");
      opt.value = v.id;
      opt.textContent = v.name;
      vaultSelect.appendChild(opt);
    }

    // Restore previously selected vault
    const savedVaultId = sessionStorage.getItem("oc_vault_id");
    if (savedVaultId && vaults.some((v) => v.id === savedVaultId)) {
      vaultSelect.value = savedVaultId;
    }

    currentVaultId = vaultSelect.value;
    sessionStorage.setItem("oc_vault_id", currentVaultId);
    await loadVault(currentVaultId);
  } catch (e: any) {
    console.error("Failed to load vaults:", e);
    // If auth failed, redirect to login
    if (e?.message?.includes("401") || e?.message?.includes("unauthorized")) {
      doLogout();
    }
  }
}

async function onVaultChange() {
  disconnectSync();
  editor?.close();
  currentVaultId = vaultSelect.value;
  sessionStorage.setItem("oc_vault_id", currentVaultId);
  crdt = new CRDTManager();
  if (editor) {
    editor.destroy();
    editor = new Editor(editorEl, emptyStateEl, crdt, {
      onContentChange: () => {
        syncClient?.pushChanges();
        scheduleCRDTSave();
      },
    });
  }
  await loadVault(currentVaultId);
}

async function loadVault(vaultId: string) {
  if (!vaultId) return;

  // Try to restore CRDT state
  loadCRDTState(vaultId);

  // Refresh file tree from CRDT
  refreshFileTree();

  // Connect to sync
  connectSync(vaultId);
}

// ── File operations ──

function openFile(path: string) {
  if (!editor) return;
  editor.openFile(path);
  currentFileEl.textContent = path;
}

function onNewFile() {
  const name = prompt("File name (e.g. notes/new-note.md):");
  if (!name) return;

  const path = name.endsWith(".md") ? name : name + ".md";
  crdt.createFile(path, "");
  syncClient?.pushChanges();
  refreshFileTree();
  openFile(path);
  scheduleCRDTSave();
}

function refreshFileTree() {
  const files = crdt.getAllFiles().filter((p) => {
    const info = crdt.getFileInfo(p);
    return info && !info.is_binary;
  });
  fileTree?.render(files);

  const activePath = editor?.getCurrentPath();
  if (activePath) {
    fileTree?.setActive(activePath);
  }
}

// ── CRDT sync ──

function connectSync(vaultId: string) {
  const token = api.getAccessToken();
  if (!token) return;

  syncClient = new SyncClient(
    window.location.origin,
    vaultId,
    token,
    crdt,
    {
      onPatchesApplied: (patches) => onRemotePatches(patches),
      onConnected: () => updateSyncStatus("connected"),
      onDisconnected: () => updateSyncStatus("disconnected"),
      onError: (err) => {
        console.error("Sync error:", err);
        updateSyncStatus("error");
      },
    }
  );
  syncClient.connect();
}

function disconnectSync() {
  if (syncClient) {
    syncClient.disconnect();
    syncClient = null;
  }
  updateSyncStatus("disconnected");
}

function onRemotePatches(patches: Patch[]) {
  // Determine affected file paths
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

  // Update file tree if structure changed
  if (deletedPaths.size > 0 || affectedPaths.size > 0) {
    refreshFileTree();
  }

  // Update editor if current file changed
  if (editor) {
    const currentPath = editor.getCurrentPath();
    if (currentPath && deletedPaths.has(currentPath)) {
      editor.close();
      currentFileEl.textContent = "";
    } else {
      editor.applyRemoteChanges(affectedPaths);
    }
  }

  scheduleCRDTSave();
}

function updateSyncStatus(status: string) {
  syncStatus.textContent = status;
  syncStatus.className = "sync-badge";
  if (status === "connected") syncStatus.classList.add("connected");
  else if (status === "error") syncStatus.classList.add("error");
}

// ── CRDT persistence (sessionStorage) ──

let saveTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleCRDTSave() {
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => saveCRDTState(), 3000);
}

function saveCRDTState() {
  if (!currentVaultId) return;
  try {
    const data = crdt.save();
    const b64 = uint8ArrayToBase64(data);
    sessionStorage.setItem(`oc_crdt_${currentVaultId}`, b64);
  } catch (e) {
    console.error("Failed to save CRDT state:", e);
  }
}

function loadCRDTState(vaultId: string) {
  try {
    const b64 = sessionStorage.getItem(`oc_crdt_${vaultId}`);
    if (b64) {
      const data = base64ToUint8Array(b64);
      crdt.load(data);
      console.log("Loaded CRDT state from sessionStorage");
    }
  } catch (e) {
    console.warn("Failed to load CRDT state:", e);
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

// ── Start ──

init();
