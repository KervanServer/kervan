import type {
  ApiFileEntry,
  ApiSession,
  ApiShareLink,
  ApiKey,
  ApiTransfer,
  ApiUser,
  AuditEvent,
  LoginResponse,
  ServerStatus,
} from "@/lib/types"

const asMessage = (value: unknown): string => {
  if (typeof value === "string") {
    return value
  }
  return "Request failed"
}

async function request<T>(url: string, token: string | null, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  if (!headers.has("Content-Type") && init?.body) {
    headers.set("Content-Type", "application/json")
  }
  if (token) {
    headers.set("Authorization", `Bearer ${token}`)
  }

  const res = await fetch(url, { ...init, headers })
  if (res.status === 401) {
    throw new Error("Session expired")
  }

  const contentType = res.headers.get("content-type") || ""
  const isJSON = contentType.includes("application/json")
  const payload = isJSON ? await res.json() : await res.text()
  if (!res.ok) {
    if (isJSON && payload && typeof payload === "object" && "error" in payload) {
      throw new Error(asMessage((payload as { error: unknown }).error))
    }
    throw new Error(typeof payload === "string" ? payload : "Request failed")
  }

  return payload as T
}

export const api = {
  login(username: string, password: string): Promise<LoginResponse> {
    return request<LoginResponse>("/api/v1/auth/login", null, {
      method: "POST",
      body: JSON.stringify({ username, password }),
    })
  },

  status(token: string): Promise<ServerStatus> {
    return request<ServerStatus>("/api/v1/server/status", token)
  },

  metricsRaw(token: string): Promise<string> {
    return request<string>("/api/v1/metrics", token)
  },

  serverConfig(token: string): Promise<{ config: Record<string, unknown> }> {
    return request<{ config: Record<string, unknown> }>("/api/v1/server/config", token)
  },

  updateServerConfig(token: string, patch: Record<string, unknown>): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>("/api/v1/server/config", token, {
      method: "PUT",
      body: JSON.stringify(patch),
    })
  },

  validateServerConfig(token: string, patch: Record<string, unknown>): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>("/api/v1/server/config/validate", token, {
      method: "POST",
      body: JSON.stringify(patch),
    })
  },

  reloadServer(token: string): Promise<Record<string, unknown>> {
    return request<Record<string, unknown>>("/api/v1/server/reload", token, { method: "POST" })
  },

  users(token: string): Promise<{ users: ApiUser[] }> {
    return request<{ users: ApiUser[] }>("/api/v1/users", token)
  },

  apiKeys(token: string): Promise<{ keys: ApiKey[] }> {
    return request<{ keys: ApiKey[] }>("/api/v1/apikeys", token)
  },

  createApiKey(token: string, payload: { name: string; permissions: string }): Promise<{ key: string; id: string }> {
    return request<{ key: string; id: string }>("/api/v1/apikeys", token, {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  deleteApiKey(token: string, id: string): Promise<void> {
    return request<void>(`/api/v1/apikeys?id=${encodeURIComponent(id)}`, token, { method: "DELETE" })
  },

  createUser(token: string, payload: { username: string; password: string; home_dir: string; admin: boolean }): Promise<void> {
    return request<void>("/api/v1/users", token, {
      method: "POST",
      body: JSON.stringify(payload),
    })
  },

  updateUser(token: string, payload: { id: string; enabled?: boolean; home_dir?: string; admin?: boolean }): Promise<void> {
    return request<void>("/api/v1/users", token, {
      method: "PUT",
      body: JSON.stringify(payload),
    })
  },

  deleteUser(token: string, id: string): Promise<void> {
    return request<void>(`/api/v1/users?id=${encodeURIComponent(id)}`, token, { method: "DELETE" })
  },

  sessions(token: string): Promise<{ sessions: ApiSession[] }> {
    return request<{ sessions: ApiSession[] }>("/api/v1/sessions", token)
  },

  files(token: string, targetPath: string): Promise<{ path: string; entries: ApiFileEntry[] }> {
    return request<{ path: string; entries: ApiFileEntry[] }>(
      `/api/v1/files/me/ls?path=${encodeURIComponent(targetPath)}`,
      token,
    )
  },

  mkdir(token: string, targetPath: string): Promise<void> {
    return request<void>(`/api/v1/files/me/mkdir?path=${encodeURIComponent(targetPath)}`, token, { method: "POST" })
  },

  remove(token: string, targetPath: string, recursive = false): Promise<void> {
    return request<void>(
      `/api/v1/files/me/rm?path=${encodeURIComponent(targetPath)}&recursive=${recursive ? "true" : "false"}`,
      token,
      { method: "DELETE" },
    )
  },

  rename(token: string, fromPath: string, toPath: string): Promise<void> {
    return request<void>(
      `/api/v1/files/me/rename?from=${encodeURIComponent(fromPath)}&to=${encodeURIComponent(toPath)}`,
      token,
      { method: "POST" },
    )
  },

  createShareLink(token: string, targetPath: string, ttl = "24h"): Promise<{ token: string; share_url: string; expires_at: string }> {
    return request<{ token: string; share_url: string; expires_at: string }>(
      `/api/v1/files/me/share?path=${encodeURIComponent(targetPath)}&ttl=${encodeURIComponent(ttl)}`,
      token,
      { method: "POST" },
    )
  },

  shareLinks(token: string): Promise<{ links: ApiShareLink[] }> {
    return request<{ links: ApiShareLink[] }>("/api/v1/share", token)
  },

  revokeShareLink(token: string, shareToken: string): Promise<void> {
    return request<void>(`/api/v1/share?token=${encodeURIComponent(shareToken)}`, token, { method: "DELETE" })
  },

  upload(token: string, targetPath: string, file: File): Promise<void> {
    return request<void>(`/api/v1/files/me/upload?path=${encodeURIComponent(targetPath)}`, token, {
      method: "POST",
      headers: { "Content-Type": "application/octet-stream" },
      body: file,
    })
  },

  transfers(token: string, page = 1, limit = 50): Promise<{ active: ApiTransfer[]; recent: ApiTransfer[]; stats: Record<string, unknown> }> {
    return request<{ active: ApiTransfer[]; recent: ApiTransfer[]; stats: Record<string, unknown> }>(
      `/api/v1/transfers?page=${page}&limit=${limit}`,
      token,
    )
  },

  audit(token: string, page = 1, limit = 100): Promise<{ events: AuditEvent[]; pagination: Record<string, unknown> }> {
    return request<{ events: AuditEvent[]; pagination: Record<string, unknown> }>(
      `/api/v1/audit/events?page=${page}&limit=${limit}`,
      token,
    )
  },
}
