import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { GroupsPage } from "@/pages/groups-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    groups: vi.fn(),
    createGroup: vi.fn(),
    updateGroup: vi.fn(),
    deleteGroup: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

const readOnly = { upload: false, download: true, delete: false, rename: false, create_dir: false, list_dir: true, chmod: false }

describe("GroupsPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.groups.mockResolvedValue({
      groups: [
        {
          id: "g-1",
          name: "auditors",
          description: "Read-only reviewers",
          permissions: readOnly,
          max_storage: -1,
          member_count: 2,
          created_at: "2026-10-01T00:00:00Z",
          updated_at: "2026-10-01T00:00:00Z",
        },
      ],
    })
    mockedAPI.createGroup.mockResolvedValue({} as never)
    mockedAPI.updateGroup.mockResolvedValue({} as never)
    mockedAPI.deleteGroup.mockResolvedValue(undefined)
  })

  it("lists groups with members, quota and permissions", async () => {
    renderWithProviders(<GroupsPage token="t" />)
    const row = (await screen.findByText("auditors")).closest("tr")!
    expect(within(row).getByText("2")).toBeInTheDocument()
    expect(within(row).getByText("Unlimited")).toBeInTheDocument()
    expect(within(row).getByText("List, Download")).toBeInTheDocument()
  })

  it("validates the name and creates a group", async () => {
    const user = userEvent.setup()
    renderWithProviders(<GroupsPage token="t" />)
    await screen.findByText("auditors")

    const create = screen.getByRole("button", { name: "Create group" })
    await user.type(screen.getByLabelText("Group name"), "bad name")
    expect(screen.getByText(/Use 1-64 letters/)).toBeInTheDocument()
    expect(create).toBeDisabled()

    await user.clear(screen.getByLabelText("Group name"))
    await user.type(screen.getByLabelText("Group name"), "staff")
    await user.click(screen.getByLabelText("Delete"))
    await user.click(create)

    await waitFor(() =>
      expect(mockedAPI.createGroup).toHaveBeenCalledWith("t", {
        name: "staff",
        description: "",
        permissions: { upload: true, download: true, delete: false, rename: true, create_dir: true, list_dir: true, chmod: false },
        max_storage: 0,
      }),
    )
  })

  it("force-deletes a group that has members after confirmation", async () => {
    const user = userEvent.setup()
    renderWithProviders(<GroupsPage token="t" />)
    await user.click(await screen.findByRole("button", { name: "Delete group auditors" }))
    expect(await screen.findByText(/has 2 member/)).toBeInTheDocument()
    await user.click(screen.getByRole("button", { name: "Delete group" }))
    await waitFor(() => expect(mockedAPI.deleteGroup).toHaveBeenCalledWith("t", "g-1", true))
  })
})
