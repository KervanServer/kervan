import { screen } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { MonitoringPage } from "@/pages/monitoring-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    status: vi.fn(),
    metricsRaw: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

describe("MonitoringPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.status.mockResolvedValue({
      uptime_seconds: 1234,
      tls_certificate: {
        source: "acme",
        status: "valid",
        issuer: "Test CA",
        subject: "CN=localhost",
        dns_names: ["localhost", "example.test"],
        serial_number: "abc123",
        not_after: "2026-05-12T10:00:00Z",
        expires_in_seconds: 86400,
      },
    })
    mockedAPI.metricsRaw.mockResolvedValue(`
# HELP example metrics
kervan_users_total 8
kervan_sessions_active 3
kervan_transfers_active 1
kervan_transfer_upload_bytes_total 2048
kervan_transfer_download_bytes_total 4096
kervan_auth_locked_accounts 0
kervan_users_admin_total 1
kervan_users_disabled_total 2
kervan_goroutines 25
kervan_memory_bytes 1024
kervan_transfers_total 6
kervan_transfers_failed_total 1
kervan_transfers_completed_total 5
    `)
  })

  it("renders parsed metrics and tls details", async () => {
    renderWithProviders(<MonitoringPage token="token-123" />)

    expect(await screen.findByRole("heading", { level: 1, name: "Monitoring" })).toBeInTheDocument()
    expect(await screen.findByText("TLS Certificate")).toBeInTheDocument()
    expect(screen.getByText(/Source: acme/)).toBeInTheDocument()
    expect(screen.getByRole("cell", { name: "kervan_users_total" })).toBeInTheDocument()
    expect(screen.getAllByText("8").length).toBeGreaterThan(0)
  })
})
