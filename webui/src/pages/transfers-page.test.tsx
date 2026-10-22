import { screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { TransfersPage } from "@/pages/transfers-page"
import { api } from "@/lib/api"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiTransfer } from "@/lib/types"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    transfers: vi.fn(),
  },
}))

vi.mock("@/lib/use-live-snapshot", () => ({
  useLiveSnapshot: vi.fn(),
}))

const mockedAPI = vi.mocked(api)
const mockedUseLiveSnapshot = vi.mocked(useLiveSnapshot)

function transfer(overrides: Partial<ApiTransfer> & Pick<ApiTransfer, "id" | "username">): ApiTransfer {
  return {
    protocol: "sftp",
    direction: "upload",
    path: "/data/report.txt",
    status: "active",
    bytes_total: 2048,
    bytes_transferred: 1024,
    speed_bps: 512,
    started_at: "2026-10-22T10:00:00Z",
    ...overrides,
  }
}

describe("TransfersPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.transfers.mockResolvedValue({
      active: [transfer({ id: "t-active", username: "alice" })],
      recent: [transfer({ id: "t-recent", username: "bob", status: "completed", path: "/data/archive.zip", completed_at: "2026-10-22T10:05:00Z" })],
      stats: {},
    })
    mockedUseLiveSnapshot.mockReturnValue({ snapshot: null, connected: true, error: null })
  })

  it("renders active and recent transfers from the query", async () => {
    renderWithProviders(<TransfersPage token="token-123" />)

    expect(await screen.findByText("alice")).toBeInTheDocument()
    expect(screen.getByText("bob")).toBeInTheDocument()
    expect(screen.getByText("1 active, 1 recent. Live stream on.")).toBeInTheDocument()
    expect(screen.getByText("Live stream")).toBeInTheDocument()
    expect(screen.getByText("/data/report.txt")).toBeInTheDocument()
    expect(screen.getByText("/data/archive.zip")).toBeInTheDocument()
    expect(screen.getByText("completed")).toBeInTheDocument()
  })

  it("prefers live snapshot rows that arrive after the query data", async () => {
    const { rerender } = renderWithProviders(<TransfersPage token="token-123" />)

    // The initial query populates the table first.
    expect(await screen.findByText("alice")).toBeInTheDocument()
    expect(screen.getByText("bob")).toBeInTheDocument()

    mockedUseLiveSnapshot.mockReturnValue({
      snapshot: {
        type: "snapshot",
        transfers: {
          active: [transfer({ id: "t-live", username: "carol-live" })],
          recent: [],
        },
      },
      connected: true,
      error: null,
    })
    rerender(<TransfersPage token="token-123" />)

    expect(await screen.findByText("carol-live")).toBeInTheDocument()
    expect(screen.queryByText("alice")).not.toBeInTheDocument()
    expect(screen.queryByText("bob")).not.toBeInTheDocument()
  })

  it("shows the empty state when there are no transfers", async () => {
    mockedAPI.transfers.mockResolvedValue({ active: [], recent: [], stats: {} })
    mockedUseLiveSnapshot.mockReturnValue({ snapshot: null, connected: false, error: null })

    renderWithProviders(<TransfersPage token="token-123" />)

    expect(await screen.findByText("No transfers yet")).toBeInTheDocument()
    expect(screen.getByText("Snapshot mode")).toBeInTheDocument()
  })

  it("surfaces query errors", async () => {
    mockedAPI.transfers.mockRejectedValue(new Error("transfers unavailable"))

    renderWithProviders(<TransfersPage token="token-123" />)

    expect(await screen.findByText("transfers unavailable")).toBeInTheDocument()
  })
})
