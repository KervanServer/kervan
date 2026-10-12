import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { AuditPage } from "@/pages/audit-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: { audit: vi.fn(), exportAudit: vi.fn() },
  RequestError: class RequestError extends Error {},
  setUnauthorizedHandler: vi.fn(),
}))
vi.mock("@/lib/use-live-snapshot", () => ({
  useLiveSnapshot: () => ({ snapshot: null, connected: false, error: null }),
}))

const mockedAPI = vi.mocked(api)

describe("AuditPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.audit.mockResolvedValue({
      events: [
        { id: "1", type: "share.download", status: "ok", username: "ann", protocol: "share", ip: "203.0.113.9", path: "/r.pdf", timestamp: "2026-10-02T10:00:00Z", meta: { bytes: "13", seq: "4" } },
      ],
      pagination: { page: 1, page_size: 50, total: 120 },
    })
    mockedAPI.exportAudit.mockResolvedValue({ blob: new Blob(["[]"]), filename: "audit.json" })
  })

  it("applies date and type filters to the list and the export", async () => {
    const user = userEvent.setup()
    renderWithProviders(<AuditPage token="t" />)

    expect(await screen.findByText("share.download")).toBeInTheDocument()
    expect(screen.getByText("ann · share · 203.0.113.9")).toBeInTheDocument()
    expect(screen.getByText("bytes=13")).toBeInTheDocument() // chain fields hidden
    expect(screen.getByText("Page 1 of 3")).toBeInTheDocument()

    await user.type(screen.getByLabelText("From date"), "2026-10-01")
    await user.type(screen.getByLabelText("To date"), "2026-10-03")
    await user.type(screen.getByLabelText("Event type"), "share.download")
    await user.click(screen.getByRole("button", { name: "Apply" }))
    await waitFor(() =>
      expect(mockedAPI.audit).toHaveBeenLastCalledWith(
        "t",
        expect.objectContaining({ from: "2026-10-01", to: "2026-10-03", type: "share.download" }),
        1,
        50,
      ),
    )

    URL.createObjectURL = vi.fn(() => "blob:x")
    URL.revokeObjectURL = vi.fn()
    await user.click(screen.getByRole("button", { name: "Export JSON" }))
    expect(mockedAPI.exportAudit).toHaveBeenCalledWith("t", "json", expect.objectContaining({ from: "2026-10-01", type: "share.download" }))

    await user.click(screen.getByRole("button", { name: "Older" }))
    await waitFor(() => expect(mockedAPI.audit).toHaveBeenLastCalledWith("t", expect.anything(), 2, 50))
  })
})
