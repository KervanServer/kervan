import { screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { DashboardPage } from "@/pages/dashboard-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    status: vi.fn(),
    totpStatus: vi.fn(),
    totpSetup: vi.fn(),
    totpEnable: vi.fn(),
    totpDisable: vi.fn(),
  },
}))

vi.mock("@/lib/use-live-snapshot", () => ({
  useLiveSnapshot: vi.fn(),
}))

import { useLiveSnapshot } from "@/lib/use-live-snapshot"

const mockedAPI = vi.mocked(api)
const mockedUseLiveSnapshot = vi.mocked(useLiveSnapshot)

describe("DashboardPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.status.mockResolvedValue({
      active_sessions: 3,
      active_transfers: 1,
      upload_bytes: 2048,
      download_bytes: 4096,
    })
    mockedAPI.totpStatus.mockResolvedValue({
      enabled: false,
      pending: false,
    })
    mockedAPI.totpSetup.mockResolvedValue({
      enabled: false,
      pending: true,
      secret: "totp-secret",
      otpauth_url: "otpauth://totp/Kervan:test",
      username: "admin",
      issuer: "Kervan",
      generated_at: "2026-04-12T10:00:00Z",
    })
    mockedAPI.totpEnable.mockResolvedValue({
      enabled: true,
      pending: false,
    })
    mockedAPI.totpDisable.mockResolvedValue({
      disabled: true,
    })
    mockedUseLiveSnapshot.mockReturnValue({
      snapshot: null,
      connected: true,
      error: null,
    })
  })

  it("renders the initial server snapshot", async () => {
    renderWithProviders(<DashboardPage token="token-123" />)

    expect(await screen.findByText("Server Snapshot")).toBeInTheDocument()
    expect(screen.getByText(/"active_sessions": 3/)).toBeInTheDocument()
    expect(screen.getByText(/"download_bytes": 4096/)).toBeInTheDocument()
  })

})
