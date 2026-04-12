import { MemoryRouter } from "react-router-dom"
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"

import { AppShell } from "@/components/app-shell"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/components/theme-toggle", () => ({
  ThemeToggle: () => <div>Theme toggle</div>,
}))

vi.mock("@/lib/route-modules", () => ({
  prefetchRoute: vi.fn(),
}))

import { prefetchRoute } from "@/lib/route-modules"

const mockedPrefetchRoute = vi.mocked(prefetchRoute)

describe("AppShell", () => {
  it("prefetches route modules on hover and focus", async () => {
    const user = userEvent.setup()

    renderWithProviders(
      <MemoryRouter>
        <AppShell currentUser="alice" onLogout={vi.fn()} />
      </MemoryRouter>,
    )

    await user.hover(screen.getByRole("link", { name: "Users" }))
    expect(mockedPrefetchRoute).toHaveBeenCalledWith("/users")

    screen.getByRole("link", { name: "Files" }).focus()
    expect(mockedPrefetchRoute).toHaveBeenCalledWith("/files")
  })
})
