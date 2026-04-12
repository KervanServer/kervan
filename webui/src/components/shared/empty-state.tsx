import type { LucideIcon } from "lucide-react"

import { Button } from "@/components/ui/button"

type EmptyStateProps = {
  title: string
  description: string
  icon: LucideIcon
  actionLabel?: string
  onAction?: () => void
}

export function EmptyState({ title, description, icon: Icon, actionLabel, onAction }: EmptyStateProps) {
  return (
    <div className="rounded-2xl border border-dashed border-[var(--border-strong)] bg-[var(--background-subtle)] px-6 py-10 text-center">
      <div className="mx-auto flex h-12 w-12 items-center justify-center rounded-full bg-[var(--accent-muted)] text-[var(--accent)]">
        <Icon className="h-5 w-5" />
      </div>
      <h3 className="mt-4 text-lg font-semibold tracking-tight">{title}</h3>
      <p className="mt-2 text-sm leading-relaxed text-[var(--text-secondary)]">{description}</p>
      {actionLabel && onAction ? (
        <div className="mt-4">
          <Button variant="outline" onClick={onAction}>
            {actionLabel}
          </Button>
        </div>
      ) : null}
    </div>
  )
}
