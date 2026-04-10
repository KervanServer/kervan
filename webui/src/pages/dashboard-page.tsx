import { useEffect, useMemo, useState } from "react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { StatsGrid } from "@/components/stats-grid"
import { api } from "@/lib/api"
import type { TOTPSetupResponse, TOTPStatus } from "@/lib/types"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"

type Props = {
  token: string
}

export function DashboardPage({ token }: Props) {
  const [status, setStatus] = useState<Record<string, unknown>>({})
  const [error, setError] = useState<string | null>(null)
  const [totpStatus, setTotpStatus] = useState<TOTPStatus | null>(null)
  const [totpSetup, setTotpSetup] = useState<TOTPSetupResponse | null>(null)
  const [totpCode, setTotpCode] = useState("")
  const [totpBusy, setTotpBusy] = useState<string | null>(null)
  const [totpError, setTotpError] = useState<string | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["server", "sessions", "transfers"])

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const [nextStatus, nextTOTP] = await Promise.all([api.status(token), api.totpStatus(token)])
        if (!cancelled) {
          setStatus(nextStatus)
          setTotpStatus(nextTOTP)
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

  const refreshTOTPStatus = async () => {
    const next = await api.totpStatus(token)
    setTotpStatus(next)
    return next
  }

  const beginTOTPSetup = async () => {
    setTotpBusy("setup")
    try {
      const setup = await api.totpSetup(token)
      setTotpSetup(setup)
      setTotpStatus({ enabled: setup.enabled, pending: setup.pending })
      setTotpCode("")
      setTotpError(null)
    } catch (err) {
      setTotpError(err instanceof Error ? err.message : "Unable to prepare two-factor setup")
    } finally {
      setTotpBusy(null)
    }
  }

  const enableTOTP = async () => {
    setTotpBusy("enable")
    try {
      const next = await api.totpEnable(token, totpCode)
      setTotpStatus(next)
      setTotpSetup(null)
      setTotpCode("")
      setTotpError(null)
    } catch (err) {
      setTotpError(err instanceof Error ? err.message : "Unable to enable two-factor auth")
    } finally {
      setTotpBusy(null)
    }
  }

  const disableTOTP = async () => {
    setTotpBusy("disable")
    try {
      await api.totpDisable(token, totpCode)
      setTotpSetup(null)
      setTotpCode("")
      setTotpError(null)
      await refreshTOTPStatus()
    } catch (err) {
      setTotpError(err instanceof Error ? err.message : "Unable to disable two-factor auth")
    } finally {
      setTotpBusy(null)
    }
  }

  return (
    <section className="space-y-4">
      <StatsGrid {...data} />

      <Card className="fade-up" style={{ animationDelay: "100ms" }}>
        <CardHeader>
          <CardTitle>Two-Factor Authentication</CardTitle>
          <CardDescription>Protect the Web UI login with a time-based one-time password.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-sm text-[var(--muted-foreground)]">
            Status: {totpStatus?.enabled ? "Enabled" : totpStatus?.pending ? "Setup pending" : "Disabled"}
          </p>
          {totpError ? <p className="text-sm text-[var(--destructive)]">{totpError}</p> : null}
          {!totpStatus?.enabled ? (
            <Button variant="outline" onClick={() => void beginTOTPSetup()} disabled={totpBusy !== null}>
              {totpBusy === "setup" ? "Preparing..." : "Prepare TOTP setup"}
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
                <Button variant="destructive" onClick={() => void disableTOTP()} disabled={totpBusy !== null}>
                  {totpBusy === "disable" ? "Disabling..." : "Disable TOTP"}
                </Button>
              ) : (
                <Button onClick={() => void enableTOTP()} disabled={totpBusy !== null}>
                  {totpBusy === "enable" ? "Enabling..." : "Enable TOTP"}
                </Button>
              )}
            </div>
          ) : null}
        </CardContent>
      </Card>

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
