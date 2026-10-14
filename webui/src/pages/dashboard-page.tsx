import { useEffect, useMemo, useState } from "react"
import { RefreshCw, ShieldCheck } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { ConnectionBanner } from "@/components/shared/connection-banner"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useDashboardStatus } from "@/hooks/use-dashboard"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { StatsGrid } from "@/components/stats-grid"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"

type Props = {
  token: string
}

export function DashboardPage({ token }: Props) {
  const serverStatusQuery = useDashboardStatus(token)

  const [status, setStatus] = useState<Record<string, unknown>>({})
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["server", "sessions", "transfers"])

  useEffect(() => {
    if (serverStatusQuery.data) {
      setStatus(serverStatusQuery.data)
    }
  }, [serverStatusQuery.data])

  useEffect(() => {
    if (snapshot?.server) {
      setStatus(snapshot.server)
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

  const isLoading = serverStatusQuery.isLoading
  const error = serverStatusQuery.error instanceof Error ? serverStatusQuery.error.message : null
  const isRefreshing = serverStatusQuery.isFetching

  const refreshDashboard = async () => {
    await serverStatusQuery.refetch()
  }

  if (isLoading) {
    return (
      <section className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, index) => (
            <Skeleton key={index} className="h-28 w-full" />
          ))}
        </div>
        <Skeleton className="h-80 w-full" />
        <Skeleton className="h-96 w-full" />
      </section>
    )
  }

  if (Object.keys(status).length === 0) {
    return (
      <EmptyState
        title="No dashboard snapshot"
        description="The server did not return a runtime status snapshot yet."
        icon={ShieldCheck}
      />
    )
  }

  return (
    <section className="space-y-4">
      <PageHeader
        title="Dashboard"
        description="Review live service health and transfer activity. Two-factor sign-in is under My Account."
        actions={
          <>
            <Badge variant="outline">{connected ? "Live stream" : "Snapshot mode"}</Badge>
            <Button variant="outline" onClick={() => void refreshDashboard()} disabled={isRefreshing}>
              <RefreshCw className={`mr-2 h-4 w-4 ${isRefreshing ? "animate-spin motion-reduce:animate-none" : ""}`} />
              {isRefreshing ? "Refreshing..." : "Refresh"}
            </Button>
          </>
        }
      />

      <ConnectionBanner message={liveError} onRetry={() => void refreshDashboard()} />

      <StatsGrid {...data} />

      <Card className="fade-up">
        <CardHeader>
          <CardTitle>Server Snapshot</CardTitle>
          <CardDescription>{connected ? "Live via WebSocket /api/v1/ws." : "Fallback snapshot mode."}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error">{error}</StatusMessage> : null}
          <pre className="max-h-[50vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
            {JSON.stringify(status, null, 2)}
          </pre>
        </CardContent>
      </Card>
    </section>
  )
}
