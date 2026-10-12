import { render, screen } from "@testing-library/react"
import { afterEach, describe, expect, it, vi } from "vitest"

import { SharePage } from "@/pages/share-page"
import { matchShareToken } from "@/lib/share-route"

function mockInfo(body: object, ok = true) {
  vi.stubGlobal("fetch", vi.fn(async () => ({ ok, json: async () => body })))
}

describe("SharePage", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    window.history.replaceState(null, "", "/")
  })

  it("matches only share routes", () => {
    expect(matchShareToken("/s/share_abc-DEF_1")).toBe("share_abc-DEF_1")
    expect(matchShareToken("/s/share_abc/")).toBe("share_abc")
    expect(matchShareToken("/s/")).toBeNull()
    expect(matchShareToken("/files")).toBeNull()
    expect(matchShareToken("/s/a/b")).toBeNull()
  })

  it("posts the password with a native form for protected links", async () => {
    window.history.replaceState(null, "", "/s/tok?error=password_required")
    mockInfo({ name: "report.pdf", size: 2048, expires_at: "2026-10-20T00:00:00Z", password_protected: true, expired: false, exhausted: false, downloads_remaining: 2 })
    const { container } = render(<SharePage token="tok" />)

    expect(await screen.findByText("report.pdf")).toBeInTheDocument()
    expect(screen.getByText("2.0 KB")).toBeInTheDocument()
    expect(screen.getByText("That password is not correct.")).toBeInTheDocument()
    const form = container.querySelector("form")!
    expect(form.getAttribute("method")).toBe("post")
    expect(form.getAttribute("action")).toBe("/api/v1/share/tok")
    expect(screen.getByLabelText("Password").getAttribute("name")).toBe("password")
  })

  it("links straight to the file when unprotected, and explains dead links", async () => {
    mockInfo({ name: "a.txt", expires_at: "2026-10-20T00:00:00Z", password_protected: false, expired: false, exhausted: false })
    const { unmount } = render(<SharePage token="tok" />)
    expect((await screen.findByRole("link", { name: /Download/ })).getAttribute("href")).toBe("/api/v1/share/tok")
    unmount()

    mockInfo({ name: "a.txt", expires_at: "2026-10-01T00:00:00Z", password_protected: false, expired: true, exhausted: false })
    render(<SharePage token="tok" />)
    expect(await screen.findByText("This link has expired.")).toBeInTheDocument()
    expect(screen.queryByRole("link", { name: /Download/ })).toBeNull()
  })
})
