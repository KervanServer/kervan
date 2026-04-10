import { useEffect, useMemo, useState } from "react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { StatsGrid } from "@/components/stats-grid"
import { api } from "@/lib/api"

type Props = {
  token: string
}

export function DashboardPage({ token }: Props) {
  const [status, setStatus] = useState<Record<string, unknown>>({})
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const next = await api.status(token)
        if (!cancelled) {
          setStatus(next)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Failed to load status")
        }
      }
    }

    void load()
    const id = window.setInterval(() => {
      void load()
    }, 10000)

    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [token])

  const data = useMemo(() => {
    const activeSessions = Number(status.active_sessions ?? 0)
    const activeTransfers = Number(status.active_transfers ?? 0)
    const uploadBytes = Number(status.upload_bytes ?? 0)
    const downloadBytes = Number(status.download_bytes ?? 0)
    return { activeSessions, activeTransfers, uploadBytes, downloadBytes }
  }, [status])

  return (
    <section className="space-y-4">
      <StatsGrid {...data} />

      <Card className="fade-up" style={{ animationDelay: "140ms" }}>
        <CardHeader>
          <CardTitle>Server Snapshot</CardTitle>
          <CardDescription>Auto-refreshes every 10 seconds.</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
          <pre className="max-h-[50vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
            {JSON.stringify(status, null, 2)}
          </pre>
        </CardContent>
      </Card>
    </section>
  )
}


