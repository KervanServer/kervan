import { useEffect, useState } from "react"
import { Loader2 } from "lucide-react"

import { StatusMessage } from "@/components/shared/status-message"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { useDisableTOTP, useEnableTOTP, usePrepareTOTPSetup, useTOTPStatus } from "@/hooks/use-dashboard"
import type { TOTPSetupResponse, TOTPStatus } from "@/lib/types"

/** WebUI two-factor (TOTP) setup for the signed-in user. */
export function TwoFactorCard({ token }: { token: string }) {
  const totpStatusQuery = useTOTPStatus(token)
  const prepareTOTPSetupMutation = usePrepareTOTPSetup(token)
  const enableTOTPMutation = useEnableTOTP(token)
  const disableTOTPMutation = useDisableTOTP(token)
  const [totpStatus, setTotpStatus] = useState<TOTPStatus | null>(null)
  const [totpSetup, setTotpSetup] = useState<TOTPSetupResponse | null>(null)
  const [totpCode, setTotpCode] = useState("")

  useEffect(() => {
    if (totpStatusQuery.data) {
      setTotpStatus(totpStatusQuery.data)
    }
  }, [totpStatusQuery.data])

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

  const isBusy =
    prepareTOTPSetupMutation.isPending || enableTOTPMutation.isPending || disableTOTPMutation.isPending

  if (totpStatusQuery.isLoading) {
    return <Skeleton className="h-40 w-full" />
  }

  return (
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
  )
}
