import { useEffect, useMemo, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiSession } from "@/lib/types"

type Props = { token: string }

export function SessionsPage({ token }: Props) {
  const [sessions, setSessions] = useState<ApiSession[]>([])
  const [selectedSession, setSelectedSession] = useState<ApiSession | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [killingId, setKillingId] = useState<string | null>(null)
  const [query, setQuery] = useState("")
  const [protocolFilter, setProtocolFilter] = useState("")
  const [ipFilter, setIPFilter] = useState("")
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["sessions"])

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const data = await api.sessions(token)
        if (!cancelled) {
          setSessions(data.sessions)
          setSelectedSession((current) =>
            current ? data.sessions.find((item) => item.id === current.id) ?? current : data.sessions[0] ?? null,
          )
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
      const nextSessions = snapshot.sessions
      setSessions(nextSessions)
      setSelectedSession((current) =>
        current ? nextSessions.find((item) => item.id === current.id) ?? current : nextSessions[0] ?? null,
      )
    }
  }, [snapshot])

  const filteredSessions = useMemo(() => {
    return sessions
      .filter((session) => (protocolFilter ? session.protocol.toLowerCase() === protocolFilter.toLowerCase() : true))
      .filter((session) => (ipFilter ? session.remote_addr.toLowerCase().includes(ipFilter.toLowerCase()) : true))
      .filter((session) => {
        if (!query) {
          return true
        }
        const haystack = [session.id, session.username, session.protocol, session.remote_addr].join(" ").toLowerCase()
        return haystack.includes(query.toLowerCase())
      })
      .sort((left, right) => right.started_at.localeCompare(left.started_at))
  }, [ipFilter, protocolFilter, query, sessions])

  useEffect(() => {
    if (!selectedSession && filteredSessions.length > 0) {
      setSelectedSession(filteredSessions[0])
      return
    }
    if (selectedSession && !filteredSessions.some((item) => item.id === selectedSession.id)) {
      setSelectedSession(filteredSessions[0] ?? null)
    }
  }, [filteredSessions, selectedSession])

  const onKill = async (session: ApiSession) => {
    if (!window.confirm(`Disconnect session ${session.id}?`)) {
      return
    }
    setKillingId(session.id)
    try {
      await api.killSession(token, session.id)
      setSessions((current) => current.filter((item) => item.id !== session.id))
      setSelectedSession((current) => (current?.id === session.id ? null : current))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to disconnect session")
    } finally {
      setKillingId(null)
    }
  }

  return (
    <section className="grid gap-4 xl:grid-cols-[1fr_320px]">
      <Card>
        <CardHeader>
          <CardTitle>Live Sessions</CardTitle>
          <CardDescription>{connected ? "Live updates are active." : "Fallback mode."}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
          {liveError ? <p className="mb-3 text-sm text-[var(--destructive)]">{liveError}</p> : null}
          <div className="mb-4 grid gap-2 md:grid-cols-3">
            <Input placeholder="Search user, protocol, id..." value={query} onChange={(e) => setQuery(e.target.value)} />
            <Input placeholder="Filter protocol" value={protocolFilter} onChange={(e) => setProtocolFilter(e.target.value)} />
            <Input placeholder="Filter remote IP" value={ipFilter} onChange={(e) => setIPFilter(e.target.value)} />
          </div>
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
              {filteredSessions.map((session) => (
                <TableRow
                  key={session.id}
                  className={selectedSession?.id === session.id ? "bg-[var(--muted)]/50" : undefined}
                  onClick={() => setSelectedSession(session)}
                >
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
                      onClick={(event) => {
                        event.stopPropagation()
                        void onKill(session)
                      }}
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

      <Card>
        <CardHeader>
          <CardTitle>Session Detail</CardTitle>
          <CardDescription>{selectedSession ? selectedSession.id : "Select a session from the table."}</CardDescription>
        </CardHeader>
        <CardContent>
          {selectedSession ? (
            <div className="space-y-3 text-sm">
              <DetailRow label="User" value={selectedSession.username} />
              <DetailRow label="Protocol" value={selectedSession.protocol} />
              <DetailRow label="Remote" value={selectedSession.remote_addr} />
              <DetailRow label="Started" value={new Date(selectedSession.started_at).toLocaleString()} />
              <DetailRow label="Last seen" value={new Date(selectedSession.last_seen_at).toLocaleString()} />
            </div>
          ) : (
            <p className="text-sm text-[var(--muted-foreground)]">No session selected.</p>
          )}
        </CardContent>
      </Card>
    </section>
  )
}

function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--muted)]/40 p-3">
      <p className="text-xs text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-1 break-all font-medium">{value}</p>
    </div>
  )
}
