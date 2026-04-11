import type {
  ApiFileEntry,
  ApiSession,
  ApiShareLink,
  ApiKeysResponse,
  ApiTransfer,
  ApiUserImportReport,
  ApiUser,
  AuditEvent,
  LoginResponse,
  ServerStatus,
  TOTPSetupResponse,
  TOTPStatus,
} from "@/lib/types"

export class RequestError extends Error {
  code?: string

  constructor(message: string, code?: string) {
    super(message)
    this.name = "RequestError"
    this.code = code
  }
}

const asMessage = (value: unknown): string => {
  if (typeof value === "string") {
    return value
  }
  return "Request failed"
}

async function request<T>(url: string, token: string | null, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers)
  const body = init?.body
  const isFormData = typeof FormData !== "undefined" && body instanceof FormData
  if (!headers.has("Content-Type") && body && !isFormData) {
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
      const typed = payload as { error: unknown; code?: unknown }
      throw new RequestError(asMessage(typed.error), typeof typed.code === "string" ? typed.code : undefined)
    }
    throw new RequestError(typeof payload === "string" ? payload : "Request failed")
  }

  return payload as T
}

async function requestBlob(
  url: string,
  token: string | null,
  init?: RequestInit,
): Promise<{ blob: Blob; filename: string | null }> {
  const headers = new Headers(init?.headers)
  if (token) {
    headers.set("Authorization", `Bearer ${token}`)
  }

  const res = await fetch(url, { ...init, headers })
  if (res.status === 401) {
    throw new Error("Session expired")
  }
  if (!res.ok) {
    const contentType = res.headers.get("content-type") || ""
    if (contentType.includes("application/json")) {
      const payload = (await res.json()) as { error?: unknown }
      throw new Error(asMessage(payload.error))
    }
    throw new Error(await res.text())
  }

  const disposition = res.headers.get("content-disposition") || ""
  const match = disposition.match(/filename="([^"]+)"/i)
  return {
    blob: await res.blob(),
    filename: match?.[1] ?? null,
  }
}

export const api = {
  async login(username: string, password: string, otp?: string): Promise<LoginResponse> {
    const res = await fetch("/api/v1/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username, password, otp }),
    })
    const payload = (await res.json()) as LoginResponse & { error?: unknown; code?: unknown }
    if (!res.ok) {
      throw new RequestError(
        asMessage(payload.error),
        typeof payload.code === "string" ? payload.code : undefined,
      )
    }
    return payload
  },

  totpStatus(token: string): Promise<TOTPStatus> {
    return request<TOTPStatus>("/api/v1/auth/totp", token)
  },

  totpSetup(token: string): Promise<TOTPSetupResponse> {
    return request<TOTPSetupResponse>("/api/v1/auth/totp/setup", token, { method: "POST" })
  },

  totpEnable(token: string, code: string): Promise<TOTPStatus> {
    return request<TOTPStatus>("/api/v1/auth/totp/enable", token, {
      method: "POST",
      body: JSON.stringify({ code }),
    })
  },

  totpDisable(token: string, code?: string): Promise<{ disabled: boolean }> {
    return request<{ disabled: boolean }>("/api/v1/auth/totp", token, {
      method: "DELETE",
      body: JSON.stringify({ code }),
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

  importUsers(token: string, file: File): Promise<ApiUserImportReport> {
    const formData = new FormData()
    formData.set("file", file)
    return request<ApiUserImportReport>("/api/v1/users/import", token, {
      method: "POST",
      body: formData,
    })
  },

  exportUsers(token: string, format: "json" | "csv"): Promise<{ blob: Blob; filename: string | null }> {
    return requestBlob(`/api/v1/users/export?format=${encodeURIComponent(format)}`, token)
  },

  apiKeys(token: string): Promise<ApiKeysResponse> {
    return request<ApiKeysResponse>("/api/v1/apikeys", token)
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

  session(token: string, id: string): Promise<ApiSession> {
    return request<ApiSession>(`/api/v1/sessions/${encodeURIComponent(id)}`, token)
  },

  killSession(token: string, id: string): Promise<void> {
    return request<void>(`/api/v1/sessions/${encodeURIComponent(id)}`, token, { method: "DELETE" })
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

  exportAudit(token: string, format: "json" | "csv"): Promise<{ blob: Blob; filename: string | null }> {
    return requestBlob(`/api/v1/audit/export?format=${encodeURIComponent(format)}`, token)
  },
}
