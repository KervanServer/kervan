import { act, renderHook } from "@testing-library/react"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { useLiveSnapshot } from "@/lib/use-live-snapshot"

type FakeHandlers = {
  onopen: (() => void) | null
  onmessage: ((event: { data: unknown }) => void) | null
  onerror: (() => void) | null
  onclose: (() => void) | null
}

class FakeWebSocket {
  static instances: Array<FakeWebSocket> = []

  url: string
  protocols: string[]
  close = vi.fn()
  onopen: (() => void) | null = null
  onmessage: ((event: { data: unknown }) => void) | null = null
  onerror: (() => void) | null = null
  onclose: (() => void) | null = null

  constructor(url: string, protocols: string[]) {
    this.url = url
    this.protocols = protocols
    FakeWebSocket.instances.push(this)
  }

  asHandlers(): FakeHandlers {
    return this
  }
}

function currentSocket(): FakeWebSocket {
  const socket = FakeWebSocket.instances.at(-1)
  if (!socket) {
    throw new Error("no WebSocket was constructed")
  }
  return socket
}

function deliver(socket: FakeWebSocket, payload: unknown) {
  socket.onmessage?.({ data: JSON.stringify(payload) })
}

describe("useLiveSnapshot", () => {
  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal("WebSocket", FakeWebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("connects with subprotocol auth and encoded types", () => {
    const { result } = renderHook(() => useLiveSnapshot("tok-123", ["server", "sessions"]))

    expect(result.current.snapshot).toBeNull()
    const socket = currentSocket()
    expect(socket.url).toBe(`ws://${window.location.host}/api/v1/ws?types=${encodeURIComponent("server,sessions")}`)
    expect(socket.protocols).toEqual(["kervan.v1", "auth.tok-123"])
  })

  it("normalizes, lowercases and dedupes requested types", () => {
    renderHook(() => useLiveSnapshot("tok-123", ["SESSIONS", "  sessions ", "sessions", ""]))

    expect(currentSocket().url).toBe(`ws://${window.location.host}/api/v1/ws?types=sessions`)
  })

  it("opens no socket without a token", () => {
    const { result } = renderHook(() => useLiveSnapshot("", ["sessions"]))

    expect(FakeWebSocket.instances).toHaveLength(0)
    expect(result.current.connected).toBe(false)
  })

  it("tracks connection state and keeps only snapshot messages", () => {
    const { result } = renderHook(() => useLiveSnapshot("tok-123", ["sessions"]))

    act(() => currentSocket().onopen?.())
    expect(result.current.connected).toBe(true)
    expect(result.current.error).toBeNull()

    act(() => deliver(currentSocket(), { type: "snapshot", sessions: [{ id: "s1" }] }))
    expect(result.current.snapshot?.type).toBe("snapshot")
    expect(result.current.snapshot?.sessions).toHaveLength(1)

    act(() => deliver(currentSocket(), { type: "heartbeat" }))
    expect(result.current.snapshot?.sessions).toHaveLength(1)

    act(() => currentSocket().onmessage?.({ data: "{not json" }))
    expect(result.current.snapshot?.sessions).toHaveLength(1)

    act(() => currentSocket().onerror?.())
    expect(result.current.error).toBe("Live stream unavailable")

    act(() => currentSocket().onclose?.())
    expect(result.current.connected).toBe(false)
  })

  it("closes the socket on unmount", () => {
    const { unmount } = renderHook(() => useLiveSnapshot("tok-123", ["audit"]))
    const socket = currentSocket()

    unmount()

    expect(socket.close).toHaveBeenCalled()
  })
})
