import { useState } from "react"

import type { ApiPermissions } from "@/lib/types"
import { Input } from "@/components/ui/input"

export const PERMISSION_FIELDS: { key: keyof Pick<ApiPermissions, "upload" | "download" | "delete" | "rename" | "create_dir" | "list_dir" | "chmod">; label: string }[] = [
  { key: "list_dir", label: "List" },
  { key: "download", label: "Download" },
  { key: "upload", label: "Upload" },
  { key: "create_dir", label: "Create folders" },
  { key: "rename", label: "Rename" },
  { key: "delete", label: "Delete" },
  { key: "chmod", label: "Change permissions" },
]

export const DEFAULT_PERMISSIONS: ApiPermissions = {
  upload: true,
  download: true,
  delete: true,
  rename: true,
  create_dir: true,
  list_dir: true,
  chmod: false,
}

export function permissionSummary(p: ApiPermissions | undefined): string {
  if (!p) {
    return "-"
  }
  const enabled = PERMISSION_FIELDS.filter((field) => p[field.key]).map((field) => field.label)
  return enabled.length === 0 ? "None" : enabled.join(", ")
}

type PermissionsEditorProps = {
  value: ApiPermissions
  onChange: (next: ApiPermissions) => void
  disabled?: boolean
  idPrefix: string
}

export function PermissionsEditor({ value, onChange, disabled = false, idPrefix }: PermissionsEditorProps) {
  return (
    <fieldset className="grid grid-cols-2 gap-x-4 gap-y-1" disabled={disabled}>
      <legend className="mb-1 text-sm font-medium">Permissions</legend>
      {PERMISSION_FIELDS.map((field) => (
        <label key={field.key} htmlFor={`${idPrefix}-${field.key}`} className="flex min-h-9 items-center gap-2 text-sm">
          <input
            id={`${idPrefix}-${field.key}`}
            type="checkbox"
            checked={Boolean(value[field.key])}
            onChange={(event) => onChange({ ...value, [field.key]: event.target.checked })}
          />
          {field.label}
        </label>
      ))}
    </fieldset>
  )
}

type QuotaMode = "inherit" | "unlimited" | "custom"
const MB = 1024 * 1024
const GB = 1024 * MB

function quotaMode(value: number): QuotaMode {
  if (value === 0) return "inherit"
  if (value < 0) return "unlimited"
  return "custom"
}

type QuotaFieldProps = {
  /** Bytes; 0 inherits, -1 is unlimited. */
  value: number
  onChange: (next: number) => void
  inheritLabel: string
  idPrefix: string
}

export function QuotaField({ value, onChange, inheritLabel, idPrefix }: QuotaFieldProps) {
  const mode = quotaMode(value)
  const useGB = value >= GB && value % GB === 0
  const unit = useGB ? GB : MB
  const amount = mode === "custom" ? String(Math.round(value / unit)) : ""
  // The text is kept locally so the field can be cleared while typing; the
  // byte value only changes when the text is a positive number.
  const [text, setText] = useState(amount)
  const [syncedFrom, setSyncedFrom] = useState(value)
  if (syncedFrom !== value) {
    setSyncedFrom(value)
    if (Number.parseInt(text, 10) * unit !== value) {
      setText(amount)
    }
  }

  return (
    <div className="space-y-2">
      <label htmlFor={`${idPrefix}-quota-mode`} className="text-sm font-medium">
        Storage quota
      </label>
      <select
        id={`${idPrefix}-quota-mode`}
        className="h-10 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 text-sm"
        value={mode}
        onChange={(event) => {
          const next = event.target.value as QuotaMode
          onChange(next === "inherit" ? 0 : next === "unlimited" ? -1 : GB)
        }}
      >
        <option value="inherit">{inheritLabel}</option>
        <option value="unlimited">Unlimited</option>
        <option value="custom">Custom size</option>
      </select>
      {mode === "custom" ? (
        <div className="flex gap-2">
          <Input
            id={`${idPrefix}-quota-amount`}
            aria-label="Quota size"
            type="number"
            min={1}
            value={text}
            onChange={(event) => {
              setText(event.target.value)
              const parsed = Number.parseInt(event.target.value, 10)
              if (Number.isFinite(parsed) && parsed > 0) {
                onChange(parsed * unit)
              }
            }}
          />
          <select
            aria-label="Quota unit"
            className="h-10 rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
            value={useGB ? "GB" : "MB"}
            onChange={(event) => {
              const nextUnit = event.target.value === "GB" ? GB : MB
              onChange(Math.max(1, Math.round(value / unit)) * nextUnit)
            }}
          >
            <option value="MB">MB</option>
            <option value="GB">GB</option>
          </select>
        </div>
      ) : null}
    </div>
  )
}
