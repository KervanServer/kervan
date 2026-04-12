import { screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { ApiKeysPage } from "@/pages/apikeys-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    apiKeys: vi.fn(),
    createApiKey: vi.fn(),
    deleteApiKey: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

const fixtures = {
  keys: [
    {
      id: "key-1",
      name: "Existing key",
      permissions: "read-only",
      prefix: "krn_abc",
      created_at: "2026-04-12T10:00:00Z",
    },
  ],
  supported_scopes: [
    {
      name: "server:read",
      resource: "server",
      access: "read",
      description: "Read server status and configuration.",
    },
    {
      name: "files:read",
      resource: "files",
      access: "read",
      description: "Browse and inspect files.",
    },
    {
      name: "files:write",
      resource: "files",
      access: "write",
      description: "Upload, rename, and remove files.",
    },
  ],
  presets: [
    {
      id: "read-only",
      label: "Read Only",
      description: "Inspection-only access.",
      scopes: ["server:read", "files:read"],
    },
    {
      id: "read-write",
      label: "Read Write",
      description: "Read and mutate files.",
      scopes: ["server:read", "files:read", "files:write"],
    },
  ],
}

describe("ApiKeysPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.apiKeys.mockResolvedValue(fixtures)
    mockedAPI.createApiKey.mockResolvedValue({ id: "key-2", key: "krn_secret" })
    mockedAPI.deleteApiKey.mockResolvedValue(undefined)
  })

  it("creates keys with the selected preset identifier", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ApiKeysPage token="token-123" />)

    await screen.findByText("Read Write")
    await user.type(screen.getByPlaceholderText("Key name"), "Deploy bot")
    await user.click(screen.getByRole("button", { name: "Create key" }))

    await waitFor(() =>
      expect(mockedAPI.createApiKey).toHaveBeenCalledWith("token-123", {
        name: "Deploy bot",
        permissions: "read-write",
      }),
    )
    expect(await screen.findByText("New API key (shown once)")).toBeInTheDocument()
  })

  it("switches to custom permissions when scopes diverge from presets", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ApiKeysPage token="token-123" />)

    await screen.findByText("Read Write")
    await user.click(screen.getByRole("checkbox", { name: /server:read/i }))
    expect(screen.getByText("Custom")).toBeInTheDocument()

    await user.type(screen.getByPlaceholderText("Key name"), "Read bot")
    await user.click(screen.getByRole("button", { name: "Create key" }))

    await waitFor(() =>
      expect(mockedAPI.createApiKey).toHaveBeenCalledWith("token-123", {
        name: "Read bot",
        permissions: "files:read,files:write",
      }),
    )
  })

  it("revokes an existing key after confirmation", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ApiKeysPage token="token-123" />)

    await screen.findByText("Existing key")
    await user.click(screen.getByRole("button", { name: "Revoke API key Existing key" }))
    await user.click(await screen.findByRole("button", { name: "Revoke key" }))

    await waitFor(() => expect(mockedAPI.deleteApiKey).toHaveBeenCalledWith("token-123", "key-1"))
  })
})
