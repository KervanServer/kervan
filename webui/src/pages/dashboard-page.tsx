import { useEffect, useMemo, useState } from "react"
import { Loader2, RefreshCw, ShieldCheck } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { ConnectionBanner } from "@/components/shared/connection-banner"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import {
  useDashboardStatus,
  useDisableTOTP,
  useEnableTOTP,
  usePrepareTOTPSetup,
  useTOTPStatus,
} from "@/hooks/use-dashboard"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { StatsGrid } from "@/components/stats-grid"
import type { TOTPSetupResponse, TOTPStatus } from "@/lib/types"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"

type Props = {
  token: string
}

export function DashboardPage({ token }: Props) {
  const serverStatusQuery = useDashboardStatus(token)
  const totpStatusQuery = useTOTPStatus(token)
  const prepareTOTPSetupMutation = usePrepareTOTPSetup(token)
  const enableTOTPMutation = useEnableTOTP(token)
  const disableTOTPMutation = useDisableTOTP(token)

  const [status, setStatus] = useState<Record<string, unknown>>({})
  const [totpStatus, setTotpStatus] = useState<TOTPStatus | null>(null)
  const [totpSetup, setTotpSetup] = useState<TOTPSetupResponse | null>(null)
  const [totpCode, setTotpCode] = useState("")
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["server", "sessions", "transfers"])

  useEffect(() => {
    if (serverStatusQuery.data) {
      setStatus(serverStatusQuery.data)
    }
  }, [serverStatusQuery.data])

  useEffect(() => {
    if (totpStatusQuery.data) {
      setTotpStatus(totpStatusQuery.data)
    }
  }, [totpStatusQuery.data])

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

  const beginTOTPSetup = async () => {
    try {
      const setup = await prepareTOTPSetupMutation.mutateAsync()
      setTotpSetup(setup)
      setTotpStatus({ enabled: setup.enabled, pending: setup.pending })
      setTotpCode("")
    } catch {
      // Mutation toasts already surface the error.
    }
  }

  const enableTOTP = async () => {
    try {
      const next = await enableTOTPMutation.mutateAsync(totpCode)
      setTotpStatus(next)
      setTotpSetup(null)
      setTotpCode("")
    } catch {
      // Mutation toasts already surface the error.
    }
  }

  const disableTOTP = async () => {
    try {
      await disableTOTPMutation.mutateAsync(totpCode)
      setTotpSetup(null)
      setTotpCode("")
      setTotpStatus({ enabled: false, pending: false })
    } catch {
      // Mutation toasts already surface the error.
    }
  }

  const isLoading = serverStatusQuery.isLoading || totpStatusQuery.isLoading
  const isBusy =
    prepareTOTPSetupMutation.isPending || enableTOTPMutation.isPending || disableTOTPMutation.isPending
  const error =
    (serverStatusQuery.error instanceof Error ? serverStatusQuery.error.message : null) ??
    (totpStatusQuery.error instanceof Error ? totpStatusQuery.error.message : null)
  const isRefreshing = serverStatusQuery.isFetching || totpStatusQuery.isFetching

  const refreshDashboard = async () => {
    await Promise.all([serverStatusQuery.refetch(), totpStatusQuery.refetch()])
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
        description="Review live service health, transfer activity, and account security controls from one place."
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
          <CardTitle>Two-Factor Authentication</CardTitle>
          <CardDescription>Protect the Web UI login with a time-based one-time password.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <StatusMessage variant="info">
            Status: {totpStatus?.enabled ? "Enabled" : totpStatus?.pending ? "Setup pending" : "Disabled"}
          </StatusMessage>
          {!totpStatus?.enabled ? (
            <Button variant="outline" onClick={() => void beginTOTPSetup()} disabled={isBusy}>
              {prepareTOTPSetupMutation.isPending ? (
                <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : null}
              {prepareTOTPSetupMutation.isPending ? "Preparing..." : "Prepare TOTP setup"}
            </Button>
          ) : null}
          {totpSetup ? (
            <div className="space-y-3 rounded-xl border border-[var(--border)] bg-[var(--muted)]/40 p-3 text-sm">
              <div>
                <p className="text-xs text-[var(--muted-foreground)]">Secret</p>
                <p className="mt-1 break-all font-mono">{totpSetup.secret}</p>
              </div>
              <div>
                <p className="text-xs text-[var(--muted-foreground)]">Provisioning URL</p>
                <p className="mt-1 break-all font-mono text-xs">{totpSetup.otpauth_url}</p>
              </div>
            </div>
          ) : null}
          {totpStatus?.enabled || totpStatus?.pending ? (
            <div className="flex flex-col gap-2 md:flex-row">
              <Input
                placeholder={totpStatus.enabled ? "Enter current authenticator code to disable" : "Enter code to enable"}
                value={totpCode}
                onChange={(event) => setTotpCode(event.target.value)}
                inputMode="numeric"
              />
              {totpStatus.enabled ? (
                <Button variant="destructive" onClick={() => void disableTOTP()} disabled={isBusy || totpCode.trim() === ""}>
                  {disableTOTPMutation.isPending ? (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                  ) : null}
                  {disableTOTPMutation.isPending ? "Disabling..." : "Disable TOTP"}
                </Button>
              ) : (
                <Button onClick={() => void enableTOTP()} disabled={isBusy || totpCode.trim() === ""}>
                  {enableTOTPMutation.isPending ? (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                  ) : null}
                  {enableTOTPMutation.isPending ? "Enabling..." : "Enable TOTP"}
                </Button>
              )}
            </div>
          ) : null}
        </CardContent>
      </Card>

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
