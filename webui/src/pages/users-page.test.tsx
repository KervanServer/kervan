import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { UsersPage } from "@/pages/users-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    users: vi.fn(),
    createUser: vi.fn(),
    updateUser: vi.fn(),
    deleteUser: vi.fn(),
    importUsers: vi.fn(),
    exportUsers: vi.fn(),
    groups: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

const fixtures = {
  users: [
    {
      id: "user-1",
      username: "alice",
      type: "admin",
      enabled: true,
      home_dir: "/srv/alice",
      updated_at: "2026-04-12T10:00:00Z",
    },
    {
      id: "user-2",
      username: "bob",
      type: "user",
      enabled: false,
      home_dir: "/srv/bob",
      updated_at: "2026-04-12T11:00:00Z",
    },
  ],
}

describe("UsersPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.users.mockResolvedValue(fixtures)
    mockedAPI.groups.mockResolvedValue({
      groups: [
        {
          id: "g-1",
          name: "readers",
          permissions: { upload: false, download: true, delete: false, rename: false, create_dir: false, list_dir: true, chmod: false },
          max_storage: 0,
          member_count: 0,
          created_at: "2026-10-01T00:00:00Z",
          updated_at: "2026-10-01T00:00:00Z",
        },
      ],
    })
    mockedAPI.createUser.mockResolvedValue(undefined)
    mockedAPI.updateUser.mockResolvedValue(undefined)
    mockedAPI.deleteUser.mockResolvedValue(undefined)
    mockedAPI.importUsers.mockResolvedValue({
      format: "csv",
      total: 1,
      created: 1,
      skipped: 0,
    })
    mockedAPI.exportUsers.mockResolvedValue({
      blob: new Blob(["[]"], { type: "application/json" }),
      filename: "users.json",
    })
  })

  it("creates a user and reloads the list", async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage token="token-123" />)

    await screen.findByText("alice")
    await user.type(screen.getByPlaceholderText("Username"), "carol")
    await user.type(screen.getByPlaceholderText("Password"), "StrongPass123!")
    await user.clear(screen.getByPlaceholderText("Home directory"))
    await user.type(screen.getByPlaceholderText("Home directory"), "/srv/carol")
    await user.click(screen.getByLabelText("Administrator"))
    await user.click(screen.getByRole("button", { name: "Create" }))

    await waitFor(() =>
      expect(mockedAPI.createUser).toHaveBeenCalledWith("token-123", {
        username: "carol",
        password: "StrongPass123!",
        home_dir: "/srv/carol",
        admin: true,
      }),
    )
    expect(mockedAPI.users).toHaveBeenCalledTimes(2)
  })

  it("toggles the enabled flag for a user", async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage token="token-123" />)

    await screen.findByText("alice")
    await user.click(screen.getByRole("button", { name: "Disable user alice" }))

    await waitFor(() =>
      expect(mockedAPI.updateUser).toHaveBeenCalledWith("token-123", {
        id: "user-1",
        enabled: false,
      }),
    )
  })

  it("deletes a user after confirmation", async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage token="token-123" />)

    await screen.findByText("bob")
    await user.click(screen.getByRole("button", { name: "Delete user bob" }))
    await user.click(await screen.findByRole("button", { name: "Delete user" }))

    await waitFor(() => expect(mockedAPI.deleteUser).toHaveBeenCalledWith("token-123", "user-2"))
  })

  it("assigns a group and quota from the edit dialog", async () => {
    const user = userEvent.setup()
    renderWithProviders(<UsersPage token="token-123" />)

    await screen.findByText("bob")
    await user.click(screen.getByRole("button", { name: "Edit user bob" }))
    const dialog = await screen.findByRole("dialog")
    await user.selectOptions(within(dialog).getByLabelText("Primary group"), "readers")
    // The group's template replaces the per-user permission editor.
    expect(within(dialog).getByText(/Permissions from/)).toBeInTheDocument()
    await user.selectOptions(within(dialog).getByLabelText("Storage quota"), "custom")
    await user.clear(within(dialog).getByLabelText("Storage quota amount"))
    await user.type(within(dialog).getByLabelText("Storage quota amount"), "2")
    await user.click(within(dialog).getByRole("button", { name: "Save" }))

    await waitFor(() =>
      expect(mockedAPI.updateUser).toHaveBeenCalledWith(
        "token-123",
        expect.objectContaining({ id: "user-2", primary_group: "readers", max_storage: 2 * 1024 * 1024 * 1024, custom_permissions: false }),
      ),
    )
  })
})
