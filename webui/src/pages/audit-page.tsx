import { useEffect, useMemo, useState } from "react"
import { ChevronLeft, ChevronRight, ClipboardList, Download, Loader2, X } from "lucide-react"
import { toast } from "sonner"

import { ConnectionBanner } from "@/components/shared/connection-banner"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useAudit } from "@/hooks/use-audit"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { AuditEvent, AuditFilters } from "@/lib/types"

type Props = { token: string }

const PAGE_SIZE = 50

/** Common event types; the field also accepts any other type. */
const EVENT_TYPES = [
  "auth.success",
  "auth.failure",
  "auth.password_change",
  "file.read",
  "file.write",
  "file.delete",
  "file.rename",
  "dir.create",
  "dir.delete",
  "share.create",
  "share.download",
  "share.revoke",
  "session.revoked",
  "connection.rejected",
  "admin.user.create",
  "admin.user.update",
  "admin.user.delete",
  "admin.config.update",
]

const EMPTY: AuditFilters = { from: "", to: "", type: "", username: "", status: "", q: "" }

const str = (value: unknown) => (value === undefined || value === null ? "" : String(value))

function statusVariant(status: string): "secondary" | "outline" {
  return status === "ok" ? "secondary" : "outline"
}

export function AuditPage({ token }: Props) {
  const [draft, setDraft] = useState<AuditFilters>(EMPTY)
  const [filters, setFilters] = useState<AuditFilters>(EMPTY)
  const [page, setPage] = useState(1)
  const auditQuery = useAudit(token, filters, page)
  const [exporting, setExporting] = useState<"json" | "csv" | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["audit"])
  const [liveEvents, setLiveEvents] = useState<AuditEvent[] | null>(null)

  const filtering = useMemo(() => Object.values(filters).some((v) => v && v.trim() !== ""), [filters])
  // The live stream only mirrors the unfiltered first page.
  const live = !filtering && page === 1

  useEffect(() => {
    if (live && snapshot?.audit?.events) {
      setLiveEvents(snapshot.audit.events.slice(0, PAGE_SIZE))
    }
  }, [snapshot, live])

  const events = (live && liveEvents) || auditQuery.data?.events || []
  const total = auditQuery.data?.pagination.total ?? 0
  const pages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  const apply = (next: AuditFilters) => {
    if (next.from && next.to && next.from > next.to) {
      toast.error("The start date must not be after the end date.")
      return
    }
    setFilters(next)
    setPage(1)
    setLiveEvents(null)
  }

  const onExport = async (format: "json" | "csv") => {
    setExporting(format)
    try {
      const { blob, filename } = await api.exportAudit(token, format, filters)
      const url = URL.createObjectURL(blob)
      const link = document.createElement("a")
      link.href = url
      link.download = filename ?? `audit-export.${format}`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      toast.success(`Audit log exported as ${format.toUpperCase()}.`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Unable to export audit events")
    } finally {
      setExporting(null)
    }
  }

  const error = auditQuery.error instanceof Error ? auditQuery.error.message : null
  const field = (key: keyof AuditFilters) => ({
    value: draft[key] ?? "",
    onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setDraft({ ...draft, [key]: event.target.value }),
  })

  return (
    <section className="space-y-4">
      <PageHeader
        title="Audit"
        description="Review structured security and activity events, then export them for compliance or incident response."
        actions={
          <>
            <Badge variant="outline">{live && connected ? "Live stream" : filtering ? "Filtered" : "Snapshot mode"}</Badge>
            {(["csv", "json"] as const).map((format) => (
              <Button key={format} variant="outline" disabled={exporting !== null} onClick={() => void onExport(format)}>
                {exporting === format ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                ) : (
                  <Download className="mr-2 h-4 w-4" />
                )}
                {exporting === format ? "Exporting..." : `Export ${format.toUpperCase()}`}
              </Button>
            ))}
          </>
        }
      />

      <ConnectionBanner message={liveError} />

      <Card>
        <CardHeader>
          <CardTitle>Filters</CardTitle>
          <CardDescription>Dates are UTC; the end date includes the whole day. Exports use the same filters.</CardDescription>
        </CardHeader>
        <CardContent>
          <form
            className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4"
            onSubmit={(event) => {
              event.preventDefault()
              apply(draft)
            }}
          >
            <label className="space-y-1 text-sm">
              <span>From</span>
              <Input type="date" aria-label="From date" {...field("from")} />
            </label>
            <label className="space-y-1 text-sm">
              <span>To</span>
              <Input type="date" aria-label="To date" {...field("to")} />
            </label>
            <label className="space-y-1 text-sm">
              <span>Event type</span>
              <Input list="audit-event-types" placeholder="Any" aria-label="Event type" {...field("type")} />
              <datalist id="audit-event-types">
                {EVENT_TYPES.map((type) => (
                  <option key={type} value={type} />
                ))}
              </datalist>
            </label>
            <label className="space-y-1 text-sm">
              <span>Status</span>
              <select
                aria-label="Status"
                className="h-10 w-full rounded-md border border-[var(--border)] bg-[var(--surface)] px-3 text-sm"
                {...field("status")}
              >
                <option value="">Any</option>
                <option value="ok">ok</option>
                <option value="failed">failed</option>
                <option value="rejected">rejected</option>
                <option value="incomplete">incomplete</option>
                <option value="aborted">aborted</option>
              </select>
            </label>
            <label className="space-y-1 text-sm">
              <span>User</span>
              <Input placeholder="Any" aria-label="User" {...field("username")} />
            </label>
            <label className="space-y-1 text-sm sm:col-span-1 lg:col-span-2">
              <span>Search</span>
              <Input placeholder="Path, message, protocol…" aria-label="Search" {...field("q")} />
            </label>
            <div className="flex items-end gap-2">
              <Button type="submit">Apply</Button>
              {filtering ? (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setDraft(EMPTY)
                    apply(EMPTY)
                  }}
                >
                  <X className="mr-2 h-4 w-4" />
                  Clear
                </Button>
              ) : null}
            </div>
          </form>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Audit Log</CardTitle>
          <CardDescription>
            {auditQuery.isLoading ? "Loading..." : `${total.toLocaleString()} matching events, newest first.`}{" "}
            {live ? (connected ? "Live stream on." : "Fallback mode.") : null}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}
          {auditQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 4 }).map((_, index) => (
                <Skeleton key={index} className="h-16 w-full" />
              ))}
            </div>
          ) : events.length === 0 ? (
            <EmptyState
              title={filtering ? "No matching events" : "No audit events"}
              description={filtering ? "Widen the date range or clear some filters." : "Structured security and activity events will appear here as the server is used."}
              icon={ClipboardList}
            />
          ) : (
            <Tabs defaultValue="timeline" className="w-full">
              <TabsList>
                <TabsTrigger value="timeline">Timeline</TabsTrigger>
                <TabsTrigger value="json">JSON</TabsTrigger>
              </TabsList>
              <TabsContent value="timeline">
                <div className="space-y-2">
                  {events.map((event, index) => {
                    const meta = (event.meta ?? {}) as Record<string, unknown>
                    const metaText = Object.entries(meta)
                      .filter(([key]) => !["seq", "mac", "prev"].includes(key))
                      .map(([key, value]) => `${key}=${str(value)}`)
                      .join(" · ")
                    return (
                      <article key={str(event.id) || index} className="rounded-xl border border-[var(--border)] bg-[var(--muted)]/50 p-3 text-sm">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-medium">{str(event.type) || "event"}</span>
                          {event.status ? <Badge variant={statusVariant(str(event.status))}>{str(event.status)}</Badge> : null}
                          <span className="text-xs text-[var(--muted-foreground)]">
                            {event.timestamp ? new Date(str(event.timestamp)).toLocaleString() : ""}
                          </span>
                        </div>
                        <p className="mt-1 text-xs text-[var(--muted-foreground)]">
                          {[str(event.username), str(event.protocol), str(event.ip)].filter(Boolean).join(" · ")}
                        </p>
                        {event.path || event.message ? (
                          <p className="mt-1 break-all">
                            {event.path ? <span className="font-mono text-xs">{str(event.path)} </span> : null}
                            {str(event.message)}
                          </p>
                        ) : null}
                        {metaText ? <p className="mt-1 break-all text-xs text-[var(--muted-foreground)]">{metaText}</p> : null}
                      </article>
                    )
                  })}
                </div>
              </TabsContent>
              <TabsContent value="json">
                <pre className="max-h-[55vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">{JSON.stringify(events, null, 2)}</pre>
              </TabsContent>
            </Tabs>
          )}
          {pages > 1 ? (
            <nav className="mt-4 flex items-center justify-between text-sm" aria-label="Audit pages">
              <Button variant="outline" size="sm" disabled={page <= 1 || auditQuery.isFetching} onClick={() => setPage(page - 1)}>
                <ChevronLeft className="mr-1 h-4 w-4" />
                Newer
              </Button>
              <span>
                Page {page} of {pages}
              </span>
              <Button variant="outline" size="sm" disabled={page >= pages || auditQuery.isFetching} onClick={() => setPage(page + 1)}>
                Older
                <ChevronRight className="ml-1 h-4 w-4" />
              </Button>
            </nav>
          ) : null}
        </CardContent>
      </Card>
    </section>
  )
}
