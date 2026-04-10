export type AuthUser = {
  id: string
  username: string
  type: string
}

export type LoginResponse = {
  token: string
  user: AuthUser
}

export type ServerStatus = Record<string, unknown>

export type ApiUser = {
  id: string
  username: string
  type: string
  enabled: boolean
  home_dir: string
  updated_at: string
}

export type ApiUserImportError = {
  row: number
  username?: string
  error: string
}

export type ApiUserImportReport = {
  format: string
  total: number
  created: number
  skipped: number
  usernames?: string[]
  errors?: ApiUserImportError[]
}

export type ApiSession = {
  id: string
  username: string
  protocol: string
  remote_addr: string
  connected_at: string
  last_seen_at: string
  bytes_in: number
  bytes_out: number
}

export type ApiFileEntry = {
  name: string
  path: string
  is_dir: boolean
  size: number
  mode: number
  mod_time: string
}

export type ApiTransfer = {
  id: string
  username: string
  protocol: string
  direction: string
  path: string
  status: string
  bytes_total: number
  bytes_transferred: number
  speed_bps: number
  started_at: string
  completed_at?: string
  error?: string
}

export type AuditEvent = Record<string, unknown>

export type ApiKey = {
  id: string
  name: string
  permissions: string
  prefix: string
  created_at: string
  last_used?: string
}

export type ApiShareLink = {
  token: string
  username: string
  path: string
  created_at: string
  expires_at: string
  download_count: number
  max_downloads: number
  share_url: string
}
