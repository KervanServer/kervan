import { useEffect, useState } from "react"
import { Download } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { AuditEvent } from "@/lib/types"

type Props = { token: string }

export function AuditPage({ token }: Props) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [error, setError] = useState<string | null>(null)
  const [exporting, setExporting] = useState<"json" | "csv" | null>(null)
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["audit"])

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const data = await api.audit(token)
        if (!cancelled) {
          setEvents(data.events)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unable to load audit events")
        }
      }
    }

    void load()
    return () => {
      cancelled = true
    }
  }, [token])

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
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to export audit events")
    } finally {
      setExporting(null)
    }
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Audit Log</CardTitle>
        <CardDescription>
          Structured events from server audit sink. {connected ? "Live stream on." : "Fallback mode."}
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
        {liveError ? <p className="mb-3 text-sm text-[var(--destructive)]">{liveError}</p> : null}
        <div className="mb-4 flex gap-2">
          <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("csv")}>
            <Download className="mr-2 h-4 w-4" />
            {exporting === "csv" ? "Exporting..." : "Export CSV"}
          </Button>
          <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("json")}>
            <Download className="mr-2 h-4 w-4" />
            {exporting === "json" ? "Exporting..." : "Export JSON"}
          </Button>
        </div>
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
      </CardContent>
    </Card>
  )
}
