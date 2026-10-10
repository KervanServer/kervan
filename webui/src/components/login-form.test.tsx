import { act, screen, waitFor } from "@testing-library/react"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { LoginForm } from "@/components/login-form"
import { api } from "@/lib/api"
import { describeOIDCError, useAuthStore } from "@/stores/auth-store"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/components/theme-toggle", () => ({
  ThemeToggle: () => <div>Theme toggle</div>,
}))

vi.mock("@/lib/api", () => ({
  api: {
    authMethods: vi.fn(),
    oidcExchange: vi.fn(),
    login: vi.fn(),
  },
  RequestError: class RequestError extends Error {},
  setUnauthorizedHandler: vi.fn(),
}))

const mockedAPI = vi.mocked(api)

const renderForm = () =>
  renderWithProviders(<LoginForm onSubmit={vi.fn()} loading={false} error={null} requiresOTP={false} />)

describe("LoginForm single sign-on", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({ auth: null, authError: null, authLoading: false, requiresOTP: false })
  })

  it("shows the SSO button when OIDC is enabled", async () => {
    mockedAPI.authMethods.mockResolvedValue({
      password: true,
      oidc: { enabled: true, label: "Sign in with Acme", login_url: "/api/v1/auth/oidc/login" },
    })
    renderForm()
    const link = await screen.findByRole("link", { name: "Sign in with Acme" })
    expect(link).toHaveAttribute("href", "/api/v1/auth/oidc/login")
  })

  it("hides the SSO button when OIDC is disabled", async () => {
    mockedAPI.authMethods.mockResolvedValue({ password: true, oidc: { enabled: false } })
    renderForm()
    await waitFor(() => expect(mockedAPI.authMethods).toHaveBeenCalled())
    expect(screen.queryByRole("link", { name: /sign in with/i })).not.toBeInTheDocument()
  })

  it("exchanges a one-time code for a session", async () => {
    mockedAPI.oidcExchange.mockResolvedValue({ token: "tok", user: { id: "1", username: "ann", type: "admin" } })
    await act(() => useAuthStore.getState().completeOIDC("code-1"))
    expect(mockedAPI.oidcExchange).toHaveBeenCalledWith("code-1")
    expect(useAuthStore.getState().auth?.user.username).toBe("ann")
  })

  it("maps provider error codes to readable messages", () => {
    expect(describeOIDCError("account_conflict")).toMatch(/local Kervan account/)
    expect(describeOIDCError("weird")).toBe("Single sign-on failed (weird).")
  })
})
