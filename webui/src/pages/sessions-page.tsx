import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiSession } from "@/lib/types"

type Props = { token: string }

export function SessionsPage({ token }: Props) {
  const [sessions, setSessions] = useState<ApiSession[]>([])
  const [error, setError] = useState<string | null>(null)
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
              <TableHead>Bytes In / Out</TableHead>
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
                <TableCell>
                  {session.bytes_in} / {session.bytes_out}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}


