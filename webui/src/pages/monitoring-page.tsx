import { useEffect, useMemo, useState } from "react"

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ServerStatus } from "@/lib/types"

type Props = { token: string }

const watchedMetrics = [
  "kervan_sessions_active",
  "kervan_users_total",
  "kervan_transfers_active",
  "kervan_transfers_total",
  "kervan_transfers_completed_total",
  "kervan_transfers_failed_total",
  "kervan_transfer_upload_bytes_total",
  "kervan_transfer_download_bytes_total",
]

export function MonitoringPage({ token }: Props) {
  const [status, setStatus] = useState<ServerStatus | null>(null)
  const [metrics, setMetrics] = useState<Record<string, number>>({})
  const [error, setError] = useState<string | null>(null)
  const [updatedAt, setUpdatedAt] = useState<Date | null>(null)

  useEffect(() => {
    let cancelled = false
    const refresh = async () => {
      try {
        const [nextStatus, rawMetrics] = await Promise.all([api.status(token), api.metricsRaw(token)])
        if (cancelled) {
          return
        }
        setStatus(nextStatus)
        setMetrics(parsePrometheus(rawMetrics))
        setUpdatedAt(new Date())
        setError(null)
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unable to load monitoring data")
        }
      }
    }

    void refresh()
    const timer = window.setInterval(() => void refresh(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [token])

  const rows = useMemo(
    () =>
      watchedMetrics.map((name) => ({
        name,
        value: metrics[name] ?? 0,
      })),
    [metrics],
  )

  return (
    <section className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Monitoring</CardTitle>
          <CardDescription>Live server counters refreshed every 5 seconds.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}

          <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
            <MetricCard label="Uptime (sec)" value={formatNumber(status?.uptime_seconds)} />
            <MetricCard label="Active Sessions" value={formatNumber(metrics.kervan_sessions_active)} />
            <MetricCard label="Active Transfers" value={formatNumber(metrics.kervan_transfers_active)} />
            <MetricCard label="Total Users" value={formatNumber(metrics.kervan_users_total)} />
            <MetricCard label="Uploaded" value={formatBytes(metrics.kervan_transfer_upload_bytes_total)} />
            <MetricCard label="Downloaded" value={formatBytes(metrics.kervan_transfer_download_bytes_total)} />
            <MetricCard label="Transfers (Total)" value={formatNumber(metrics.kervan_transfers_total)} />
            <MetricCard label="Transfers (Failed)" value={formatNumber(metrics.kervan_transfers_failed_total)} />
          </div>

          <div className="rounded-xl border border-[var(--border)]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Metric</TableHead>
                  <TableHead className="text-right">Value</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row) => (
                  <TableRow key={row.name}>
                    <TableCell className="font-mono text-xs">{row.name}</TableCell>
                    <TableCell className="text-right">{formatNumber(row.value)}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>

          <p className="text-xs text-[var(--muted-foreground)]">
            Last update: {updatedAt ? updatedAt.toLocaleTimeString() : "waiting..."}
          </p>
        </CardContent>
      </Card>
    </section>
  )
}

function parsePrometheus(raw: string): Record<string, number> {
  const out: Record<string, number> = {}
  for (const line of raw.split("\n")) {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith("#")) {
      continue
    }
    const space = trimmed.lastIndexOf(" ")
    if (space <= 0) {
      continue
    }
    const key = trimmed.slice(0, space).trim()
    const value = Number(trimmed.slice(space + 1))
    if (Number.isFinite(value)) {
      out[key] = value
    }
  }
  return out
}

function MetricCard({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--card)] p-3">
      <p className="text-xs text-[var(--muted-foreground)]">{label}</p>
      <p className="mt-1 text-lg font-semibold">{value}</p>
    </div>
  )
}

function formatNumber(value: unknown): string {
  if (typeof value !== "number" || Number.isNaN(value)) {
    return "0"
  }
  return Math.round(value).toLocaleString()
}

function formatBytes(value: unknown): string {
  if (typeof value !== "number" || value <= 0 || Number.isNaN(value)) {
    return "0 B"
  }
  const units = ["B", "KB", "MB", "GB", "TB"]
  let idx = 0
  let current = value
  while (current >= 1024 && idx < units.length - 1) {
    current /= 1024
    idx++
  }
  return `${current.toFixed(current >= 10 || idx === 0 ? 0 : 1)} ${units[idx]}`
}
