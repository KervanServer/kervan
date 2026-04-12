import { useEffect, useState } from "react"
import { ClipboardList, Download, Loader2 } from "lucide-react"
import { toast } from "sonner"

import { ConnectionBanner } from "@/components/shared/connection-banner"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useAudit } from "@/hooks/use-audit"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { AuditEvent } from "@/lib/types"

type Props = { token: string }

export function AuditPage({ token }: Props) {
  const auditQuery = useAudit(token)
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [exporting, setExporting] = useState<"json" | "csv" | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["audit"])

  useEffect(() => {
    if (auditQuery.data?.events) {
      setEvents(auditQuery.data.events)
    }
  }, [auditQuery.data])

  useEffect(() => {
    if (snapshot?.audit?.events) {
      setEvents(snapshot.audit.events)
    }
  }, [snapshot])

  const onExport = async (format: "json" | "csv") => {
    setExporting(format)
    try {
      const { blob, filename } = await api.exportAudit(token, format)
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

  return (
    <section className="space-y-4">
      <PageHeader
        title="Audit"
        description="Review structured security and activity events, then export them for compliance or incident response."
        actions={
          <>
            <Badge variant="outline">{connected ? "Live stream" : "Snapshot mode"}</Badge>
            <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("csv")}>
              {exporting === "csv" ? (
                <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Download className="mr-2 h-4 w-4" />
              )}
              {exporting === "csv" ? "Exporting..." : "Export CSV"}
            </Button>
            <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("json")}>
              {exporting === "json" ? (
                <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Download className="mr-2 h-4 w-4" />
              )}
              {exporting === "json" ? "Exporting..." : "Export JSON"}
            </Button>
          </>
        }
      />

      <ConnectionBanner message={liveError} />

      <Card>
        <CardHeader>
          <CardTitle>Audit Log</CardTitle>
          <CardDescription>
            Structured events from server audit sink. {connected ? "Live stream on." : "Fallback mode."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}
          {auditQuery.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-10 w-48" />
              {Array.from({ length: 4 }).map((_, index) => (
                <Skeleton key={index} className="h-20 w-full" />
              ))}
            </div>
          ) : events.length === 0 ? (
            <EmptyState
              title="No audit events"
              description="Structured security and activity events will appear here as the server is used."
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
                  {events.map((event, index) => (
                    <article key={index} className="rounded-xl border border-[var(--border)] bg-[var(--muted)]/50 p-3 text-sm">
                      <p className="font-medium">{String(event.type ?? "event")}</p>
                      <p className="text-xs text-[var(--muted-foreground)]">{String(event.timestamp ?? "")}</p>
                      <p className="mt-1">{String(event.message ?? event.path ?? "")}</p>
                    </article>
                  ))}
                </div>
              </TabsContent>
              <TabsContent value="json">
                <pre className="max-h-[55vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
                  {JSON.stringify(events, null, 2)}
                </pre>
              </TabsContent>
            </Tabs>
          )}
        </CardContent>
      </Card>
    </section>
  )
}
