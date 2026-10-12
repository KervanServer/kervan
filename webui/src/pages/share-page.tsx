import { useEffect, useState } from "react"
import { Download, FileText, Lock, ShieldAlert } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { StatusMessage } from "@/components/shared/status-message"
import { formatBytes } from "@/lib/format"
import type { ApiShareInfo } from "@/lib/types"

const ERRORS: Record<string, string> = {
  password_required: "That password is not correct.",
  throttled: "Too many wrong attempts from your address. Try again later.",
  expired: "This link has expired.",
  exhausted: "This link has reached its download limit.",
  not_found: "This link does not exist or was revoked.",
  unavailable: "The shared file is no longer available.",
  server_error: "The server could not serve the file. Try again later.",
}

type Props = { token: string }

export function SharePage({ token }: Props) {
  const [info, setInfo] = useState<ApiShareInfo | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [password, setPassword] = useState("")
  const redirectError = new URLSearchParams(window.location.search).get("error")
  const downloadURL = `/api/v1/share/${encodeURIComponent(token)}`

  useEffect(() => {
    document.title = "Shared file · Kervan"
    let cancelled = false
    fetch(`${downloadURL}/info`)
      .then(async (res) => {
        if (cancelled) return
        if (!res.ok) {
          setLoadError(ERRORS.not_found ?? "Link not found.")
          return
        }
        setInfo((await res.json()) as ApiShareInfo)
      })
      .catch(() => !cancelled && setLoadError(ERRORS.server_error ?? "Request failed."))
    return () => {
      cancelled = true
    }
  }, [downloadURL])

  const blocked = info && (info.expired || info.exhausted || info.unavailable)
  const blockedReason = info?.expired ? ERRORS.expired : info?.exhausted ? ERRORS.exhausted : ERRORS.unavailable

  return (
    <main className="flex min-h-screen items-center justify-center bg-[var(--background)] p-4">
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            {info?.password_protected ? <Lock className="h-5 w-5" aria-hidden /> : <FileText className="h-5 w-5" aria-hidden />}
            Shared file
          </CardTitle>
          <CardDescription>Someone shared a file with you through Kervan.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {loadError ? (
            <StatusMessage variant="error">{loadError}</StatusMessage>
          ) : !info ? (
            <Skeleton className="h-24 w-full" />
          ) : (
            <>
              <dl className="grid grid-cols-[110px_1fr] gap-y-1 text-sm">
                <dt className="text-[var(--text-secondary)]">File</dt>
                <dd className="break-all font-medium">{info.name}</dd>
                {info.size !== undefined ? (
                  <>
                    <dt className="text-[var(--text-secondary)]">Size</dt>
                    <dd>{formatBytes(info.size)}</dd>
                  </>
                ) : null}
                <dt className="text-[var(--text-secondary)]">Available until</dt>
                <dd>{new Date(info.expires_at).toLocaleString()}</dd>
                {info.downloads_remaining !== undefined ? (
                  <>
                    <dt className="text-[var(--text-secondary)]">Downloads left</dt>
                    <dd>{info.downloads_remaining}</dd>
                  </>
                ) : null}
              </dl>
              {blocked ? (
                <StatusMessage variant="error">
                  <ShieldAlert className="mr-2 inline h-4 w-4" aria-hidden />
                  {blockedReason}
                </StatusMessage>
              ) : (
                <>
                  {redirectError ? <StatusMessage variant="error">{ERRORS[redirectError] ?? ERRORS.server_error}</StatusMessage> : null}
                  {info.password_protected ? (
                    // A native form post lets the browser handle the download;
                    // a wrong password redirects back here with ?error=.
                    <form method="post" action={downloadURL} className="space-y-3">
                      <label htmlFor="share-password" className="text-sm font-medium">
                        Password
                      </label>
                      <Input
                        id="share-password"
                        name="password"
                        type="password"
                        autoComplete="off"
                        required
                        autoFocus
                        value={password}
                        onChange={(event) => setPassword(event.target.value)}
                      />
                      <Button type="submit" className="w-full" disabled={password === ""}>
                        <Download className="mr-2 h-4 w-4" />
                        Download
                      </Button>
                    </form>
                  ) : (
                    <Button asChild className="w-full">
                      <a href={downloadURL} download>
                        <Download className="mr-2 h-4 w-4" />
                        Download
                      </a>
                    </Button>
                  )}
                </>
              )}
            </>
          )}
        </CardContent>
      </Card>
    </main>
  )
}
