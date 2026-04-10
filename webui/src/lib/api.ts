import type {
  ApiFileEntry,
  ApiSession,
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

  users(token: string): Promise<{ users: ApiUser[] }> {
    return request<{ users: ApiUser[] }>("/api/v1/users", token)
  },

  createUser(token: string, payload: { username: string; password: string; home_dir: string; admin: boolean }): Promise<void> {
    return request<void>("/api/v1/users", token, {
      method: "POST",
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
