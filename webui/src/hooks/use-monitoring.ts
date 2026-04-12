import { useQuery } from "@tanstack/react-query"

import { api } from "@/lib/api"

type MonitoringSnapshot = {
  status: Record<string, unknown>
  metrics: Record<string, number>
  updatedAt: string
}

export function useMonitoring(token: string) {
  return useQuery({
    queryKey: ["monitoring", token],
    queryFn: async (): Promise<MonitoringSnapshot> => {
      const [status, rawMetrics] = await Promise.all([api.status(token), api.metricsRaw(token)])
      return {
        status,
        metrics: parsePrometheus(rawMetrics),
        updatedAt: new Date().toISOString(),
      }
    },
    refetchInterval: 5000,
  })
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
