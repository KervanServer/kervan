import { useEffect, useMemo, useState } from "react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { StatsGrid } from "@/components/stats-grid"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"

type Props = {
  token: string
}

export function DashboardPage({ token }: Props) {
  const [status, setStatus] = useState<Record<string, unknown>>({})
  const [error, setError] = useState<string | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["server", "sessions", "transfers"])

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
    return () => {
      cancelled = true
    }
  }, [token])

  useEffect(() => {
    if (snapshot?.server) {
      setStatus(snapshot.server)
      setError(null)
    }
  }, [snapshot])

  const data = useMemo(() => {
    const activeSessions = snapshot?.sessions ? snapshot.sessions.length : Number(status.active_sessions ?? 0)
    const stats = snapshot?.transfers?.stats ?? status
    const activeTransfers = Number((stats as Record<string, unknown>).active_transfers ?? status.active_transfers ?? 0)
    const uploadBytes = Number((stats as Record<string, unknown>).upload_bytes ?? status.upload_bytes ?? 0)
    const downloadBytes = Number((stats as Record<string, unknown>).download_bytes ?? status.download_bytes ?? 0)
    return { activeSessions, activeTransfers, uploadBytes, downloadBytes }
  }, [snapshot, status])

  return (
    <section className="space-y-4">
      <StatsGrid {...data} />

      <Card className="fade-up" style={{ animationDelay: "140ms" }}>
        <CardHeader>
          <CardTitle>Server Snapshot</CardTitle>
          <CardDescription>{connected ? "Live via WebSocket /api/v1/ws." : "Fallback snapshot mode."}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
          {liveError ? <p className="text-sm text-[var(--destructive)]">{liveError}</p> : null}
          <pre className="max-h-[50vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
            {JSON.stringify(status, null, 2)}
          </pre>
        </CardContent>
      </Card>
    </section>
  )
}


