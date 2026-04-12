import { AlertTriangle, RefreshCw } from "lucide-react"
import type { FallbackProps } from "react-error-boundary"

import { Button } from "@/components/ui/button"

export function RouteErrorBoundary({ error, resetErrorBoundary }: FallbackProps) {
  return (
    <div className="flex min-h-[60vh] items-center justify-center">
      <div className="w-full max-w-xl rounded-3xl border border-[var(--border)] bg-[var(--surface)] p-8 shadow-lg">
        <div className="flex items-start gap-4">
          <div className="rounded-2xl bg-[var(--error-bg)] p-3 text-[var(--error)]">
            <AlertTriangle className="h-6 w-6" />
          </div>
          <div className="space-y-3">
            <div className="space-y-1">
              <h2 className="text-2xl font-semibold tracking-tight text-[var(--text-primary)]">
                Something went wrong
              </h2>
              <p className="text-sm leading-relaxed text-[var(--text-secondary)]">
                The current page crashed before it could finish rendering. You can retry the screen without leaving
                the dashboard.
              </p>
            </div>
            {error instanceof Error ? (
              <div className="rounded-2xl border border-[var(--border)] bg-[var(--background-muted)] p-3">
                <p className="font-mono text-xs text-[var(--text-secondary)]">{error.message}</p>
              </div>
            ) : null}
            <div className="flex flex-wrap items-center gap-2">
              <Button onClick={resetErrorBoundary}>
                <RefreshCw className="mr-2 h-4 w-4" />
                Retry
              </Button>
              <Button variant="outline" onClick={() => window.location.assign("/")}>
                Back to dashboard
              </Button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
