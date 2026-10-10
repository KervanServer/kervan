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

type LimitMode = "inherit" | "unlimited" | "custom"
const KB = 1024
const MB = 1024 * KB
const GB = 1024 * MB

type LimitUnit = { label: string; factor: number }
type LimitUnits = [LimitUnit, ...LimitUnit[]]
const STORAGE_UNITS: LimitUnits = [
  { label: "MB", factor: MB },
  { label: "GB", factor: GB },
]
const FILE_UNITS: LimitUnits = [{ label: "files", factor: 1 }]
const RATE_UNITS: LimitUnits = [
  { label: "KB/s", factor: KB },
  { label: "MB/s", factor: MB },
]

function limitMode(value: number): LimitMode {
  if (value === 0) return "inherit"
  if (value < 0) return "unlimited"
  return "custom"
}

type LimitFieldProps = {
  /** 0 inherits, -1 is unlimited, otherwise an amount in base units. */
  value: number
  onChange: (next: number) => void
  inheritLabel: string
  idPrefix: string
  label: string
  units: LimitUnits
}

function LimitField({ value, onChange, inheritLabel, idPrefix, label, units }: LimitFieldProps) {
  const mode = limitMode(value)
  // Prefer the largest unit that divides the value evenly.
  const unit = [...units].reverse().find((u) => value >= u.factor && value % u.factor === 0) ?? units[0]
  const amount = mode === "custom" ? String(Math.round(value / unit.factor)) : ""
  // The text is kept locally so the field can be cleared while typing; the
  // value only changes when the text is a positive number.
  const [text, setText] = useState(amount)
  const [syncedFrom, setSyncedFrom] = useState(value)
  if (syncedFrom !== value) {
    setSyncedFrom(value)
    if (Number.parseInt(text, 10) * unit.factor !== value) {
      setText(amount)
    }
  }
  const largest = units[units.length - 1] ?? units[0]

  return (
    <div className="space-y-2">
      <label htmlFor={`${idPrefix}-mode`} className="text-sm font-medium">
        {label}
      </label>
      <select
        id={`${idPrefix}-mode`}
        className="h-10 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 text-sm"
        value={mode}
        onChange={(event) => {
          const next = event.target.value as LimitMode
          onChange(next === "inherit" ? 0 : next === "unlimited" ? -1 : largest.factor)
        }}
      >
        <option value="inherit">{inheritLabel}</option>
        <option value="unlimited">Unlimited</option>
        <option value="custom">Custom</option>
      </select>
      {mode === "custom" ? (
        <div className="flex gap-2">
          <Input
            id={`${idPrefix}-amount`}
            aria-label={`${label} amount`}
            type="number"
            min={1}
            value={text}
            onChange={(event) => {
              setText(event.target.value)
              const parsed = Number.parseInt(event.target.value, 10)
              if (Number.isFinite(parsed) && parsed > 0) {
                onChange(parsed * unit.factor)
              }
            }}
          />
          <select
            aria-label={`${label} unit`}
            className="h-10 rounded-md border border-[var(--border)] bg-[var(--surface)] px-2 text-sm"
            value={unit.label}
            onChange={(event) => {
              const nextUnit = units.find((u) => u.label === event.target.value) ?? unit
              onChange(Math.max(1, Math.round(value / unit.factor)) * nextUnit.factor)
            }}
          >
            {units.map((u) => (
              <option key={u.label} value={u.label}>
                {u.label}
              </option>
            ))}
          </select>
        </div>
      ) : null}
    </div>
  )
}

type PolicyLimitProps = Omit<LimitFieldProps, "label" | "units">

export function QuotaField(props: PolicyLimitProps) {
  return <LimitField {...props} idPrefix={`${props.idPrefix}-quota`} label="Storage quota" units={STORAGE_UNITS} />
}

export function BandwidthField(props: PolicyLimitProps) {
  return <LimitField {...props} idPrefix={`${props.idPrefix}-bandwidth`} label="Bandwidth limit" units={RATE_UNITS} />
}

export function FilesField(props: PolicyLimitProps) {
  return <LimitField {...props} idPrefix={`${props.idPrefix}-files`} label="File limit" units={FILE_UNITS} />
}
