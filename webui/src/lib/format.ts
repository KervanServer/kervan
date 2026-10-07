const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"]

export function formatBytes(value: number): string {
  let current = value
  let idx = 0
  while (current >= 1024 && idx < BYTE_UNITS.length - 1) {
    current /= 1024
    idx += 1
  }
  return `${current >= 10 || idx === 0 ? current.toFixed(0) : current.toFixed(1)} ${BYTE_UNITS[idx]}`
}

/** Describes a stored quota: 0 inherits, -1 is unlimited. */
export function describeQuota(value: number | undefined, inheritLabel = "Default"): string {
  if (!value) {
    return inheritLabel
  }
  if (value < 0) {
    return "Unlimited"
  }
  return formatBytes(value)
}

/** Describes an enforced quota: 0 means unlimited. */
export function describeEffectiveQuota(value: number | undefined): string {
  return value ? formatBytes(value) : "Unlimited"
}
