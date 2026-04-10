import { useEffect, useState } from "react"

import type { ApiSession, ApiTransfer, AuditEvent } from "@/lib/types"

type LiveSnapshot = {
  type?: string
  timestamp?: string
  server?: Record<string, unknown>
  sessions?: ApiSession[]
  transfers?: {
    active?: ApiTransfer[]
    recent?: ApiTransfer[]
    stats?: Record<string, unknown>
  }
  audit?: {
    events?: AuditEvent[]
  }
}

export function useLiveSnapshot(token: string, types: string[] = ["server", "sessions", "transfers", "audit"]) {
  const [snapshot, setSnapshot] = useState<LiveSnapshot | null>(null)
  const [connected, setConnected] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const typesKey = JSON.stringify(types)

  useEffect(() => {
    if (!token) {
      return
    }

    const protocol = window.location.protocol === "https:" ? "wss" : "ws"
    const normalizedTypes = Array.from(new Set(types.map((x) => x.trim().toLowerCase()).filter((x) => x.length > 0)))
    const typesParam = normalizedTypes.join(",")
    const url = `${protocol}://${window.location.host}/api/v1/ws?token=${encodeURIComponent(token)}&types=${encodeURIComponent(typesParam)}`
    const ws = new WebSocket(url)

    ws.onopen = () => {
      setConnected(true)
      setError(null)
    }
    ws.onmessage = (event) => {
      try {
        const data = JSON.parse(event.data as string) as LiveSnapshot
        if (data && data.type === "snapshot") {
          setSnapshot(data)
        }
      } catch {
        // Ignore malformed messages.
      }
    }
    ws.onerror = () => {
      setError("Live stream unavailable")
    }
    ws.onclose = () => {
      setConnected(false)
    }

    return () => {
      ws.close()
    }
  }, [token, typesKey])

  return { snapshot, connected, error }
}
