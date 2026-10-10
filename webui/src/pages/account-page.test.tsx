import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { AccountPage } from "@/pages/account-page"
import { api } from "@/lib/api"
import { useAuthStore } from "@/stores/auth-store"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: { account: vi.fn(), changePassword: vi.fn(), setAccountKeys: vi.fn() },
  RequestError: class RequestError extends Error {},
  setUnauthorizedHandler: vi.fn(),
}))

const mockedAPI = vi.mocked(api)
const KEY = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl laptop"

const account = (overrides = {}) => ({
  id: "u1",
  username: "ann",
  type: "virtual",
  enabled: true,
  home_dir: "/ann",
  updated_at: "",
  auth_provider: "local",
  password_changeable: true,
  authorized_keys: [{ type: "ssh-ed25519", fingerprint: "SHA256:abc", comment: "laptop", line: KEY }],
  effective: { permissions: { upload: true, download: true, delete: false, rename: false, create_dir: false, list_dir: true, chmod: false }, max_storage: 0 },
  ...overrides,
})

describe("AccountPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useAuthStore.setState({ auth: { token: "old", user: { id: "u1", username: "ann", type: "virtual" } } })
    mockedAPI.account.mockResolvedValue(account())
  })

  it("changes the password and keeps the session with the new token", async () => {
    const user = userEvent.setup()
    mockedAPI.changePassword.mockResolvedValue({ token: "new-token" })
    renderWithProviders(<AccountPage token="old" />)
    await screen.findByText("ann")

    await user.type(screen.getByLabelText("Current password"), "Old-Pass-123!")
    await user.type(screen.getByLabelText("New password"), "New-Pass-456!")
    await user.type(screen.getByLabelText("Confirm new password"), "New-Pass-45")
    expect(screen.getByText("The passwords do not match.")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Change password" })).toBeDisabled()
    await user.type(screen.getByLabelText("Confirm new password"), "6!")
    await user.click(screen.getByRole("button", { name: "Change password" }))

    await waitFor(() =>
      expect(mockedAPI.changePassword).toHaveBeenCalledWith("old", { current_password: "Old-Pass-123!", new_password: "New-Pass-456!" }),
    )
    await waitFor(() => expect(useAuthStore.getState().auth?.token).toBe("new-token"))
  })

  it("hides the password form for externally managed accounts", async () => {
    mockedAPI.account.mockResolvedValue(account({ auth_provider: "oidc", password_changeable: false }))
    renderWithProviders(<AccountPage token="old" />)
    expect(await screen.findByText("Your password is managed by your identity provider.")).toBeInTheDocument()
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument()
  })

  it("adds and removes SSH keys", async () => {
    const user = userEvent.setup()
    mockedAPI.setAccountKeys.mockResolvedValue(account())
    renderWithProviders(<AccountPage token="old" />)
    await screen.findByText("SHA256:abc", { exact: false })

    await user.type(screen.getByLabelText("New SSH public keys"), "ssh-ed25519 AAAAnew ci")
    await user.click(screen.getByRole("button", { name: "Add key" }))
    await waitFor(() => expect(mockedAPI.setAccountKeys).toHaveBeenCalledWith("old", [KEY, "ssh-ed25519 AAAAnew ci"]))

    await user.click(screen.getByRole("button", { name: "Remove key laptop" }))
    await waitFor(() => expect(mockedAPI.setAccountKeys).toHaveBeenLastCalledWith("old", []))
  })
})
