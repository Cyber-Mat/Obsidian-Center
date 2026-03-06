/**
 * API client for the Obsidian Center server.
 * Handles authentication and vault operations.
 */

let baseUrl = "";
let accessToken = "";
let refreshToken = "";

export function setBaseUrl(url: string) {
  baseUrl = url.replace(/\/$/, "");
}

export function getBaseUrl(): string {
  return baseUrl;
}

export function setTokens(access: string, refresh: string) {
  accessToken = access;
  refreshToken = refresh;
  localStorage.setItem("oc_access_token", access);
  localStorage.setItem("oc_refresh_token", refresh);
}

export function loadTokens(): boolean {
  accessToken = localStorage.getItem("oc_access_token") || "";
  refreshToken = localStorage.getItem("oc_refresh_token") || "";
  return accessToken !== "";
}

export function clearTokens() {
  accessToken = "";
  refreshToken = "";
  localStorage.removeItem("oc_access_token");
  localStorage.removeItem("oc_refresh_token");
}

export function getAccessToken(): string {
  return accessToken;
}

async function apiFetch(path: string, init?: RequestInit): Promise<Response> {
  const headers: Record<string, string> = {
    ...(init?.headers as Record<string, string> || {}),
  };
  if (accessToken) {
    headers["Authorization"] = `Bearer ${accessToken}`;
  }

  const resp = await fetch(`${baseUrl}${path}`, { ...init, headers });

  // Try refresh if 401
  if (resp.status === 401 && refreshToken) {
    const refreshed = await doRefresh();
    if (refreshed) {
      headers["Authorization"] = `Bearer ${accessToken}`;
      return fetch(`${baseUrl}${path}`, { ...init, headers });
    }
  }

  return resp;
}

async function doRefresh(): Promise<boolean> {
  try {
    const resp = await fetch(`${baseUrl}/api/auth/refresh`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ refresh_token: refreshToken }),
    });
    if (!resp.ok) return false;
    const data = await resp.json();
    setTokens(data.access_token, data.refresh_token);
    return true;
  } catch {
    return false;
  }
}

// Auth

export async function login(username: string, password: string): Promise<void> {
  const resp = await fetch(`${baseUrl}/api/auth/login`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!resp.ok) {
    const err = await safeError(resp);
    throw new Error(err);
  }
  const data = await resp.json();
  setTokens(data.access_token, data.refresh_token);
}

export async function register(username: string, password: string): Promise<void> {
  const resp = await fetch(`${baseUrl}/api/auth/register`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ username, password }),
  });
  if (!resp.ok) {
    const err = await safeError(resp);
    throw new Error(err);
  }
  const data = await resp.json();
  setTokens(data.access_token, data.refresh_token);
}

// Vaults

export interface VaultInfo {
  id: string;
  name: string;
  owner_id: number;
  created_at: string;
  updated_at: string;
}

export async function listVaults(): Promise<VaultInfo[]> {
  const resp = await apiFetch("/api/vaults");
  if (!resp.ok) throw new Error("failed to list vaults");
  const data = await resp.json();
  return data.vaults || [];
}

// Files

export interface FileMeta {
  path: string;
  hash: string;
  size: number;
  is_binary: boolean;
  modified_at: string;
}

export async function listFiles(vaultId: string): Promise<FileMeta[]> {
  const resp = await apiFetch(`/api/vaults/${vaultId}/files`);
  if (!resp.ok) throw new Error("failed to list files");
  const data = await resp.json();
  return data.files || [];
}

export async function getFile(vaultId: string, path: string): Promise<string> {
  const resp = await apiFetch(`/api/vaults/${vaultId}/files/${path}`);
  if (!resp.ok) throw new Error("failed to get file");
  const data = await resp.json();
  return data.content || "";
}

async function safeError(resp: Response): Promise<string> {
  try {
    const body = await resp.json();
    return body.error || resp.statusText;
  } catch {
    return resp.statusText || `HTTP ${resp.status}`;
  }
}
