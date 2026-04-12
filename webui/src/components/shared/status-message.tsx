import type { ReactNode } from "react"
import { AlertCircle, CheckCircle2, Info } from "lucide-react"

import { cn } from "@/lib/utils"

type StatusMessageProps = {
  children: ReactNode
  className?: string
  variant?: "info" | "success" | "error"
}

const variantStyles: Record<NonNullable<StatusMessageProps["variant"]>, string> = {
  info: "border-[var(--border)] bg-[var(--background-subtle)] text-[var(--text-secondary)]",
  success: "border-[color-mix(in_srgb,var(--success)_35%,var(--border))] bg-[var(--success-bg)] text-[var(--text-primary)]",
  error: "border-[color-mix(in_srgb,var(--error)_35%,var(--border))] bg-[var(--error-bg)] text-[var(--text-primary)]",
}

const variantIcons = {
  info: Info,
  success: CheckCircle2,
  error: AlertCircle,
}

export function StatusMessage({
  children,
  className,
  variant = "info",
}: StatusMessageProps) {
  const Icon = variantIcons[variant]

  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-xl border px-3 py-2 text-sm leading-relaxed",
        variantStyles[variant],
        className,
      )}
      role={variant === "error" ? "alert" : "status"}
      aria-live={variant === "error" ? "assertive" : "polite"}
    >
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <span>{children}</span>
    </div>
  )
}
