import { useEffect, useState } from "react"
import { RefreshCw, WifiOff, X } from "lucide-react"

import { Button } from "@/components/ui/button"

type ConnectionBannerProps = {
  message: string | null
  onRetry?: () => void
}

export function ConnectionBanner({ message, onRetry }: ConnectionBannerProps) {
  const [dismissed, setDismissed] = useState(false)

  useEffect(() => {
    if (!message) {
      setDismissed(false)
      return
    }
    setDismissed(false)
  }, [message])

  if (!message || dismissed) {
    return null
  }

  return (
    <div
      className="sticky top-20 z-[100] flex flex-col gap-3 rounded-2xl border border-[var(--warning)]/40 bg-[var(--warning-bg)] px-4 py-3 shadow-sm sm:flex-row sm:items-center sm:justify-between"
      role="status"
      aria-live="polite"
    >
      <div className="flex items-start gap-3">
        <WifiOff className="mt-0.5 h-4 w-4 shrink-0 text-[var(--warning)]" />
        <div className="space-y-1">
          <p className="text-sm font-semibold tracking-tight text-[var(--text-primary)]">Connection lost</p>
          <p className="text-sm text-[var(--text-secondary)]">{message}</p>
        </div>
      </div>
      <div className="flex items-center gap-2">
        {onRetry ? (
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RefreshCw className="mr-2 h-4 w-4" />
            Retry
          </Button>
        ) : null}
        <Button variant="ghost" size="sm" onClick={() => setDismissed(true)} aria-label="Dismiss connection banner">
          <X className="h-4 w-4" />
        </Button>
      </div>
    </div>
  )
}
