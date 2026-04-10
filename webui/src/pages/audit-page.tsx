import { useEffect, useState } from "react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { api } from "@/lib/api"
import type { AuditEvent } from "@/lib/types"

type Props = { token: string }

export function AuditPage({ token }: Props) {
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [error, setError] = useState<string | null>(null)

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
    const id = window.setInterval(() => void load(), 8000)

    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [token])

  return (
    <Card>
      <CardHeader>
        <CardTitle>Audit Log</CardTitle>
        <CardDescription>Structured events from server audit sink.</CardDescription>
      </CardHeader>
      <CardContent>
        {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
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


