import { MemoryRouter } from "react-router-dom"
import { screen } from "@testing-library/react"
import type { ReactNode } from "react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { AppShell } from "@/components/app-shell"
import { renderWithProviders } from "@/test-utils"
import { useAuthStore } from "@/stores/auth-store"

vi.mock("@/components/theme-toggle", () => ({
  ThemeToggle: () => <div>Theme toggle</div>,
}))

vi.mock("@/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
}))

vi.mock("@/lib/route-modules", () => ({
  prefetchRoute: vi.fn(),
}))

import { prefetchRoute } from "@/lib/route-modules"

const mockedPrefetchRoute = vi.mocked(prefetchRoute)

describe("AppShell", () => {
  beforeEach(() => {
    useAuthStore.setState({ auth: { token: "t", user: { id: "1", username: "alice", type: "admin" } } })
  })

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

  it("shows only self-service pages to regular users", () => {
    useAuthStore.setState({ auth: { token: "t", user: { id: "2", username: "carol", type: "virtual" } } })
    renderWithProviders(
      <MemoryRouter>
        <AppShell currentUser="carol" onLogout={vi.fn()} />
      </MemoryRouter>,
    )
    for (const hidden of ["Dashboard", "Users", "Groups", "Monitoring", "Configuration"]) {
      expect(screen.queryByRole("link", { name: hidden })).toBeNull()
    }
    for (const shown of ["Files", "Sessions", "Transfers", "Audit", "API Keys", "My Account"]) {
      expect(screen.getAllByRole("link", { name: shown }).length).toBeGreaterThan(0)
    }
  })
})
