import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"

export const ACCESS_PROTOCOLS = [
  { id: "ftp", label: "FTP" },
  { id: "ftps", label: "FTPS" },
  { id: "sftp", label: "SFTP" },
  { id: "scp", label: "SCP" },
  { id: "webui", label: "Web & API" },
] as const

export type AccessDraft = {
  /** YYYY-MM-DD, or "" for never. */
  expires: string
  /** One IP or CIDR per line. */
  ips: string
  /** Empty means every protocol. */
  protocols: string[]
  max_sessions: number
}

export function accessDraftFrom(user: {
  expires_at?: string | null
  allowed_ips?: string[]
  allowed_protocols?: string[]
  max_sessions?: number
}): AccessDraft {
  return {
    expires: user.expires_at ? user.expires_at.slice(0, 10) : "",
    ips: (user.allowed_ips ?? []).join("\n"),
    protocols: [...(user.allowed_protocols ?? [])],
    max_sessions: user.max_sessions ?? 0,
  }
}

/** Converts a draft to the API patch; the expiry date ends at 23:59:59 UTC. */
export function accessPatch(draft: AccessDraft) {
  return {
    expires_at: draft.expires ? `${draft.expires}T23:59:59Z` : "",
    allowed_ips: draft.ips
      .split(/[\s,]+/)
      .map((entry) => entry.trim())
      .filter((entry) => entry !== ""),
    allowed_protocols: draft.protocols,
    max_sessions: draft.max_sessions,
  }
}

export function describeAccess(user: {
  expires_at?: string | null
  expired?: boolean
  allowed_ips?: string[]
  allowed_protocols?: string[]
  max_sessions?: number
}): string[] {
  const parts: string[] = []
  if (user.expired) parts.push("Expired")
  else if (user.expires_at) parts.push(`Expires ${user.expires_at.slice(0, 10)}`)
  if (user.allowed_ips?.length) parts.push(`${user.allowed_ips.length} IP rule${user.allowed_ips.length > 1 ? "s" : ""}`)
  if (user.allowed_protocols?.length) {
    parts.push(
      user.allowed_protocols
        .map((id) => ACCESS_PROTOCOLS.find((protocol) => protocol.id === id)?.label ?? id)
        .join(", "),
    )
  }
  if (user.max_sessions) parts.push(`≤ ${user.max_sessions} sessions`)
  return parts
}

type Props = {
  idPrefix: string
  value: AccessDraft
  onChange: (next: AccessDraft) => void
}

export function AccessFields({ idPrefix, value, onChange }: Props) {
  const toggle = (id: string, checked: boolean) => {
    const next = checked ? [...value.protocols, id] : value.protocols.filter((p) => p !== id)
    onChange({ ...value, protocols: ACCESS_PROTOCOLS.map((p) => p.id).filter((p) => next.includes(p)) })
  }
  return (
    <fieldset className="space-y-3 rounded-md border border-[var(--border)] p-3">
      <legend className="px-1 text-sm font-medium">Sign-in restrictions</legend>
      <div className="grid gap-3 sm:grid-cols-2">
        <div className="space-y-1">
          <label htmlFor={`${idPrefix}-expires`} className="text-sm">
            Account expires
          </label>
          <Input
            id={`${idPrefix}-expires`}
            type="date"
            value={value.expires}
            onChange={(event) => onChange({ ...value, expires: event.target.value })}
          />
          <p className="text-xs text-[var(--text-secondary)]">{value.expires ? "End of day, UTC" : "Never"}</p>
        </div>
        <div className="space-y-1">
          <label htmlFor={`${idPrefix}-max-sessions`} className="text-sm">
            Max concurrent sessions
          </label>
          <Input
            id={`${idPrefix}-max-sessions`}
            type="number"
            min={0}
            value={value.max_sessions}
            onChange={(event) => onChange({ ...value, max_sessions: Math.max(0, Number(event.target.value) || 0) })}
          />
          <p className="text-xs text-[var(--text-secondary)]">FTP, SFTP and SCP · 0 = unlimited</p>
        </div>
      </div>
      <div className="space-y-1">
        <label htmlFor={`${idPrefix}-ips`} className="text-sm">
          Allowed addresses
        </label>
        <Textarea
          id={`${idPrefix}-ips`}
          rows={2}
          className="font-mono text-xs"
          placeholder={"Any address\n(e.g. 203.0.113.7 or 10.0.0.0/8, one per line)"}
          value={value.ips}
          onChange={(event) => onChange({ ...value, ips: event.target.value })}
        />
      </div>
      <div className="space-y-1">
        <span className="text-sm">Allowed protocols</span>
        <div className="flex flex-wrap gap-x-4 gap-y-1">
          {ACCESS_PROTOCOLS.map((protocol) => (
            <label key={protocol.id} className="flex min-h-9 items-center gap-2 text-sm">
              <input
                type="checkbox"
                aria-label={`Allow ${protocol.label}`}
                checked={value.protocols.includes(protocol.id)}
                onChange={(event) => toggle(protocol.id, event.target.checked)}
              />
              {protocol.label}
            </label>
          ))}
        </div>
        <p className="text-xs text-[var(--text-secondary)]">
          {value.protocols.length === 0 ? "None selected: every protocol is allowed." : "Only the selected protocols are allowed."}
        </p>
      </div>
    </fieldset>
  )
}
