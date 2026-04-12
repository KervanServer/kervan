import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { FilesPage } from "@/pages/files-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    files: vi.fn(),
    shareLinks: vi.fn(),
    mkdir: vi.fn(),
    upload: vi.fn(),
    remove: vi.fn(),
    rename: vi.fn(),
    createShareLink: vi.fn(),
    revokeShareLink: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

describe("FilesPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it("creates a folder and refreshes the directory listing", async () => {
    const user = userEvent.setup()
    const entries = [
      {
        name: "notes.txt",
        path: "/notes.txt",
        is_dir: false,
        size: 128,
        mode: 420,
        mod_time: "2026-04-12T10:00:00Z",
      },
    ]

    mockedAPI.files.mockImplementation(async () => ({
      path: "/",
      entries: [...entries],
    }))
    mockedAPI.shareLinks.mockResolvedValue({ links: [] })
    mockedAPI.mkdir.mockImplementation(async (token, targetPath) => {
      expect(token).toBe("token-123")
      expect(targetPath).toBe("/reports")
      entries.push({
        name: "reports",
        path: "/reports",
        is_dir: true,
        size: 0,
        mode: 493,
        mod_time: "2026-04-12T10:05:00Z",
      })
    })

    renderWithProviders(<FilesPage token="token-123" />)

    await screen.findByRole("cell", { name: "notes.txt" })
    await user.type(screen.getByPlaceholderText("New folder"), "reports")
    await user.click(screen.getByRole("button", { name: "Create" }))

    await waitFor(() => expect(mockedAPI.mkdir).toHaveBeenCalled())
    expect(await screen.findByRole("button", { name: "reports" })).toBeInTheDocument()
  })

  it("renames a file through the dialog flow", async () => {
    const user = userEvent.setup()
    const entries = [
      {
        name: "report.txt",
        path: "/report.txt",
        is_dir: false,
        size: 256,
        mode: 420,
        mod_time: "2026-04-12T10:00:00Z",
      },
    ]

    mockedAPI.files.mockImplementation(async () => ({
      path: "/",
      entries: [...entries],
    }))
    mockedAPI.shareLinks.mockResolvedValue({ links: [] })
    mockedAPI.rename.mockImplementation(async (_token, fromPath, toPath) => {
      expect(fromPath).toBe("/report.txt")
      expect(toPath).toBe("/renamed.txt")
      entries[0] = {
        name: "renamed.txt",
        path: "/renamed.txt",
        is_dir: false,
        size: 256,
        mode: 420,
        mod_time: "2026-04-12T10:00:00Z",
      }
    })

    renderWithProviders(<FilesPage token="token-123" />)

    const row = (await screen.findByRole("cell", { name: "report.txt" })).closest("tr")
    expect(row).not.toBeNull()
    await user.click(within(row as HTMLTableRowElement).getByRole("button", { name: "Rename report.txt" }))

    const dialogInput = await screen.findByPlaceholderText("New name")
    await user.clear(dialogInput)
    await user.type(dialogInput, "renamed.txt")
    await user.click(screen.getByRole("button", { name: "Rename" }))

    await waitFor(() => expect(mockedAPI.rename).toHaveBeenCalled())
    expect(await screen.findByRole("cell", { name: "renamed.txt" })).toBeInTheDocument()
  })

  it("creates and lists a share link for a file", async () => {
    const user = userEvent.setup()
    const links = [
      {
        token: "share-1",
        username: "admin",
        path: "/notes.txt",
        created_at: "2026-04-12T10:00:00Z",
        expires_at: "2026-04-13T10:00:00Z",
        download_count: 0,
        max_downloads: 0,
        share_url: "/share/share-1",
      },
    ]

    mockedAPI.files.mockResolvedValue({
      path: "/",
      entries: [
        {
          name: "notes.txt",
          path: "/notes.txt",
          is_dir: false,
          size: 128,
          mode: 420,
          mod_time: "2026-04-12T10:00:00Z",
        },
      ],
    })
    mockedAPI.shareLinks.mockImplementation(async () => ({ links: [...links] }))
    mockedAPI.createShareLink.mockImplementation(async (_token, targetPath, ttl) => {
      expect(targetPath).toBe("/notes.txt")
      expect(ttl).toBe("48h")
      return {
        token: "share-2",
        share_url: "/share/share-2",
        expires_at: "2026-04-14T10:00:00Z",
      }
    })

    renderWithProviders(<FilesPage token="token-123" />)

    const row = (await screen.findByRole("cell", { name: "notes.txt" })).closest("tr")
    expect(row).not.toBeNull()
    await user.click(within(row as HTMLTableRowElement).getByRole("button", { name: "Create share link for notes.txt" }))

    const ttlInput = await screen.findByPlaceholderText("TTL (e.g. 24h, 7d)")
    await user.clear(ttlInput)
    await user.type(ttlInput, "48h")
    await user.click(screen.getByRole("button", { name: "Create link" }))

    await waitFor(() => expect(mockedAPI.createShareLink).toHaveBeenCalled())
    expect(await screen.findByText("Share link created")).toBeInTheDocument()
    expect(screen.getByText((content) => content.includes("/share/share-2"))).toBeInTheDocument()
    expect(screen.getByText("/notes.txt")).toBeInTheDocument()
  })
})
