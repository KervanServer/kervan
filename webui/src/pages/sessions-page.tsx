import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiSession } from "@/lib/types"

type Props = { token: string }

export function SessionsPage({ token }: Props) {
  const [sessions, setSessions] = useState<ApiSession[]>([])
  const [error, setError] = useState<string | null>(null)
  const [killingId, setKillingId] = useState<string | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["sessions"])

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const data = await api.sessions(token)
        if (!cancelled) {
          setSessions(data.sessions)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unable to load sessions")
        }
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [token])

  useEffect(() => {
    if (snapshot?.sessions) {
      setSessions(snapshot.sessions)
    }
  }, [snapshot])

  const onKill = async (session: ApiSession) => {
    if (!window.confirm(`Disconnect session ${session.id}?`)) {
      return
    }
    setKillingId(session.id)
    try {
      await api.killSession(token, session.id)
      setSessions((current) => current.filter((item) => item.id !== session.id))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to disconnect session")
    } finally {
      setKillingId(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Live Sessions</CardTitle>
        <CardDescription>{connected ? "Live updates are active." : "Fallback mode."}</CardDescription>
      </CardHeader>
      <CardContent>
        {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
        {liveError ? <p className="mb-3 text-sm text-[var(--destructive)]">{liveError}</p> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>User</TableHead>
              <TableHead>Protocol</TableHead>
              <TableHead>Remote</TableHead>
              <TableHead>Started</TableHead>
              <TableHead>Last Seen</TableHead>
              <TableHead className="text-right">Action</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {sessions.map((session) => (
              <TableRow key={session.id}>
                <TableCell>{session.username}</TableCell>
                <TableCell>
                  <Badge variant="secondary">{session.protocol}</Badge>
                </TableCell>
                <TableCell>{session.remote_addr}</TableCell>
                <TableCell>{new Date(session.started_at).toLocaleString()}</TableCell>
                <TableCell>{new Date(session.last_seen_at).toLocaleString()}</TableCell>
                <TableCell className="text-right">
                  <Button
                    size="sm"
                    variant="outline"
                    disabled={killingId === session.id}
                    onClick={() => void onKill(session)}
                  >
                    {killingId === session.id ? "Disconnecting..." : "Disconnect"}
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}
