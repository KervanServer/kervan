import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { SessionsPage } from "@/pages/sessions-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    sessions: vi.fn(),
    killSession: vi.fn(),
  },
}))

vi.mock("@/lib/use-live-snapshot", () => ({
  useLiveSnapshot: vi.fn(),
}))

import { useLiveSnapshot } from "@/lib/use-live-snapshot"

const mockedAPI = vi.mocked(api)
const mockedUseLiveSnapshot = vi.mocked(useLiveSnapshot)

const fixtures = {
  sessions: [
    {
      id: "sess-1",
      username: "alice",
      protocol: "sftp",
      remote_addr: "10.0.0.10",
      started_at: "2026-04-12T10:00:00Z",
      last_seen_at: "2026-04-12T10:05:00Z",
    },
    {
      id: "sess-2",
      username: "bob",
      protocol: "ftp",
      remote_addr: "10.0.0.20",
      started_at: "2026-04-12T11:00:00Z",
      last_seen_at: "2026-04-12T11:05:00Z",
    },
  ],
}

describe("SessionsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.sessions.mockResolvedValue(fixtures)
    mockedAPI.killSession.mockResolvedValue(undefined)
    mockedUseLiveSnapshot.mockReturnValue({
      snapshot: null,
      connected: true,
      error: null,
    })
  })

  it("filters sessions by query text", async () => {
    const user = userEvent.setup()
    renderWithProviders(<SessionsPage token="token-123" />)

    await screen.findByRole("cell", { name: "alice" })
    await user.type(screen.getByPlaceholderText("Search user, protocol, id..."), "bob")

    await waitFor(() => expect(screen.queryByRole("cell", { name: "alice" })).not.toBeInTheDocument())
    expect(screen.getByRole("cell", { name: "bob" })).toBeInTheDocument()
  })

  it("disconnects a selected session after confirmation", async () => {
    const user = userEvent.setup()
    renderWithProviders(<SessionsPage token="token-123" />)

    const aliceCell = await screen.findByRole("cell", { name: "alice" })
    const aliceRow = aliceCell.closest("tr")
    expect(aliceRow).not.toBeNull()
    await user.click(within(aliceRow as HTMLTableRowElement).getByRole("button", { name: "Disconnect session for alice" }))
    await user.click(await screen.findByRole("button", { name: "Disconnect session" }))

    await waitFor(() => expect(mockedAPI.killSession).toHaveBeenCalledWith("token-123", "sess-1"))
    await waitFor(() => expect(screen.queryByRole("cell", { name: "alice" })).not.toBeInTheDocument())
  })

  it("applies live snapshot updates to the visible session list", async () => {
    mockedUseLiveSnapshot.mockReturnValue({
      snapshot: null,
      connected: true,
      error: null,
    })

    const { rerender } = renderWithProviders(<SessionsPage token="token-123" />)

    await screen.findByRole("cell", { name: "alice" })

    mockedUseLiveSnapshot.mockReturnValue({
      snapshot: {
        sessions: [
          {
            id: "sess-3",
            username: "carol",
            protocol: "scp",
            remote_addr: "10.0.0.30",
            started_at: "2026-04-12T12:00:00Z",
            last_seen_at: "2026-04-12T12:01:00Z",
          },
        ],
      },
      connected: true,
      error: null,
    })

    rerender(<SessionsPage token="token-123" />)

    expect(await screen.findByRole("cell", { name: "carol" })).toBeInTheDocument()
    expect(screen.queryByRole("cell", { name: "alice" })).not.toBeInTheDocument()
  })
})
