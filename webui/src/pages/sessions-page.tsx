import { useEffect, useMemo, useState } from "react"
import { Activity } from "lucide-react"

import { ConfirmDialog } from "@/components/shared/confirm-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useKillSession, useSessions } from "@/hooks/use-sessions"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiSession } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip } from "@/components/ui/tooltip"

type Props = { token: string }

export function SessionsPage({ token }: Props) {
  const sessionsQuery = useSessions(token)
  const killSessionMutation = useKillSession(token)
  const [sessions, setSessions] = useState<ApiSession[]>([])
  const [selectedSession, setSelectedSession] = useState<ApiSession | null>(null)
  const [sessionToKill, setSessionToKill] = useState<ApiSession | null>(null)
  const [query, setQuery] = useState("")
  const [protocolFilter, setProtocolFilter] = useState("")
  const [ipFilter, setIPFilter] = useState("")
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["sessions"])

  useEffect(() => {
    if (!sessionsQuery.data?.sessions) {
      return
    }
    setSessions(sessionsQuery.data.sessions)
    setSelectedSession((current) =>
      current ? sessionsQuery.data?.sessions.find((item) => item.id === current.id) ?? current : sessionsQuery.data.sessions[0] ?? null,
    )
  }, [sessionsQuery.data])

  useEffect(() => {
    if (snapshot?.sessions) {
      const nextSessions = snapshot.sessions
      setSessions(nextSessions)
      setSelectedSession((current) => (current ? nextSessions.find((item) => item.id === current.id) ?? current : nextSessions[0] ?? null))
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
      setSelectedSession(filteredSessions[0] ?? null)
      return
    }
    if (selectedSession && !filteredSessions.some((item) => item.id === selectedSession.id)) {
      setSelectedSession(filteredSessions[0] ?? null)
    }
  }, [filteredSessions, selectedSession])

  const onConfirmKill = async () => {
    if (!sessionToKill) {
      return
    }
    await killSessionMutation.mutateAsync(sessionToKill.id)
    setSessions((current) => current.filter((item) => item.id !== sessionToKill.id))
    setSelectedSession((current) => (current?.id === sessionToKill.id ? null : current))
    setSessionToKill(null)
  }

  const error = sessionsQuery.error instanceof Error ? sessionsQuery.error.message : null

  return (
    <section className="grid gap-4 xl:grid-cols-[1fr_320px]">
      <div className="xl:col-span-2">
        <PageHeader
          title="Sessions"
          description="Inspect active protocol connections, filter live clients, and disconnect suspicious sessions."
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Live Sessions</CardTitle>
          <CardDescription>{connected ? "Live updates are active." : "Fallback mode."}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}
          {liveError ? <StatusMessage variant="error" className="mb-3">{liveError}</StatusMessage> : null}
          <div className="mb-4 grid gap-2 md:grid-cols-3">
            <Input placeholder="Search user, protocol, id..." value={query} onChange={(event) => setQuery(event.target.value)} />
            <Input placeholder="Filter protocol" value={protocolFilter} onChange={(event) => setProtocolFilter(event.target.value)} />
            <Input placeholder="Filter remote IP" value={ipFilter} onChange={(event) => setIPFilter(event.target.value)} />
          </div>

          {sessionsQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 4 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : filteredSessions.length === 0 ? (
            <EmptyState
              title="No live sessions"
              description="No connected FTP, FTPS, SFTP or SCP clients are active right now."
              icon={Activity}
            />
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)]">
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
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault()
                          setSelectedSession(session)
                        }
                      }}
                      tabIndex={0}
                      aria-selected={selectedSession?.id === session.id}
                    >
                      <TableCell>{session.username}</TableCell>
                      <TableCell>
                        <Badge variant="secondary">{session.protocol}</Badge>
                      </TableCell>
                      <TableCell>{session.remote_addr}</TableCell>
                      <TableCell>{new Date(session.started_at).toLocaleString()}</TableCell>
                      <TableCell>{new Date(session.last_seen_at).toLocaleString()}</TableCell>
                      <TableCell className="text-right">
                        <Tooltip content={`Disconnect ${session.username}`}>
                          <Button
                            size="sm"
                            variant="outline"
                            disabled={killSessionMutation.isPending}
                            onClick={(event) => {
                              event.stopPropagation()
                              setSessionToKill(session)
                            }}
                            aria-label={`Disconnect session for ${session.username}`}
                          >
                            Disconnect
                          </Button>
                        </Tooltip>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
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

      <ConfirmDialog
        open={sessionToKill !== null}
        title="Disconnect session"
        description={sessionToKill ? `Disconnect session "${sessionToKill.id}" for user "${sessionToKill.username}"?` : ""}
        confirmLabel="Disconnect session"
        pending={killSessionMutation.isPending}
        onConfirm={() => void onConfirmKill()}
        onOpenChange={(open) => {
          if (!open) {
            setSessionToKill(null)
          }
        }}
      />
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
