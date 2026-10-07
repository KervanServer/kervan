import { MemoryRouter } from "react-router-dom"
import { screen, within } from "@testing-library/react"
import { describe, expect, it, vi } from "vitest"

import { AppShell } from "@/components/app-shell"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/components/theme-toggle", () => ({
  ThemeToggle: () => <div>Theme toggle</div>,
}))

vi.mock("@/lib/route-modules", () => ({
  prefetchRoute: vi.fn(),
}))

// Uses the real Radix Tooltip: the compact sidebar wraps each NavLink in a
// Tooltip trigger (asChild), whose Slot once stringified NavLink's className
// callback into the class attribute.
describe("AppShell layout", () => {
  it("renders plain class names and marks the active route", () => {
    renderWithProviders(
      <MemoryRouter initialEntries={["/users"]}>
        <AppShell currentUser="alice" onLogout={vi.fn()} />
      </MemoryRouter>,
    )

    const nav = screen.getByRole("navigation", { name: "Primary navigation" })
    const links = within(nav).getAllByRole("link")
    expect(links.length).toBeGreaterThan(0)
    for (const link of links) {
      expect(link.className).not.toContain("=>")
      expect(link.className).toContain("flex")
    }
    expect(within(nav).getByRole("link", { name: "Users" }).className).toContain("bg-[var(--accent)]")
    expect(within(nav).getByRole("link", { name: "Dashboard" }).className).not.toContain("bg-[var(--accent)]")
  })

  it("renders page content inside the shell container", () => {
    const { container } = renderWithProviders(
      <MemoryRouter>
        <AppShell currentUser="alice" onLogout={vi.fn()}>
          <main data-testid="page">content</main>
        </AppShell>
      </MemoryRouter>,
    )

    const shell = container.firstElementChild
    expect(shell).not.toBeNull()
    expect(shell).toContainElement(screen.getByTestId("page"))
  })
})
