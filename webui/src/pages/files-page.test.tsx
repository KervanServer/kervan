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
    downloadFile: vi.fn(),
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
    mockedAPI.createShareLink.mockImplementation(async (_token, targetPath, ttl, options) => {
      expect(targetPath).toBe("/notes.txt")
      expect(ttl).toBe("48h")
      expect(options).toEqual({ password: "open-sesame", maxDownloads: 3 })
      return {
        token: "share-2",
        username: "alice",
        path: "/notes.txt",
        created_at: "2026-04-12T10:00:00Z",
        download_count: 0,
        max_downloads: 3,
        password_protected: true,
        share_url: "/s/share-2",
        expires_at: "2026-04-14T10:00:00Z",
      }
    })

    renderWithProviders(<FilesPage token="token-123" />)

    const row = (await screen.findByRole("cell", { name: "notes.txt" })).closest("tr")
    expect(row).not.toBeNull()
    await user.click(within(row as HTMLTableRowElement).getByRole("button", { name: "Create share link for notes.txt" }))

    const ttlInput = await screen.findByLabelText("Valid for")
    await user.clear(ttlInput)
    await user.type(ttlInput, "48h")
    await user.type(screen.getByLabelText("Password (optional)"), "open-sesame")
    const limit = screen.getByLabelText("Download limit")
    await user.clear(limit)
    await user.type(limit, "3")
    await user.click(screen.getByRole("button", { name: "Create link" }))

    await waitFor(() => expect(mockedAPI.createShareLink).toHaveBeenCalled())
    expect(await screen.findByText("Share link created")).toBeInTheDocument()
    expect(screen.getByText((content) => content.includes("/s/share-2"))).toBeInTheDocument()
    expect(screen.getByText((content) => content.includes("Password protected"))).toBeInTheDocument()
    expect(screen.getByText("/notes.txt")).toBeInTheDocument()
  })

  it("uploads several files, by picker and by drop", async () => {
    const user = userEvent.setup()
    mockedAPI.files.mockResolvedValue({ path: "/", entries: [] })
    mockedAPI.shareLinks.mockResolvedValue({ links: [] })
    mockedAPI.upload.mockResolvedValue(undefined)
    renderWithProviders(<FilesPage token="t" />)
    await screen.findByText("No files in this folder")

    await user.upload(screen.getByLabelText("Upload files"), [
      new File(["a"], "a.txt", { type: "text/plain" }),
      new File(["b"], "b.txt", { type: "text/plain" }),
    ])
    await waitFor(() => expect(mockedAPI.upload).toHaveBeenCalledTimes(2))
    expect(mockedAPI.upload.mock.calls.map((c) => c[1])).toEqual(["/a.txt", "/b.txt"])

    const dropZone = screen.getByText("File Browser").closest("div")!.parentElement!.querySelector("[aria-describedby='files-drop-hint']")!
    const dropped = new File(["c"], "c.log", { type: "text/plain" })
    const { fireEvent } = await import("@testing-library/react")
    fireEvent.drop(dropZone, { dataTransfer: { files: [dropped], types: ["Files"] } })
    await waitFor(() => expect(mockedAPI.upload).toHaveBeenCalledTimes(3))
    expect(mockedAPI.upload.mock.calls[2]?.[1]).toBe("/c.log")
  })

  it("previews text files and offers downloads", async () => {
    const user = userEvent.setup()
    mockedAPI.files.mockResolvedValue({
      path: "/",
      entries: [
        { name: "readme.md", path: "/readme.md", is_dir: false, size: 1536, mode: 420, mod_time: "2026-04-12T10:00:00Z" },
        { name: "archive.zip", path: "/archive.zip", is_dir: false, size: 99, mode: 420, mod_time: "2026-04-12T10:00:00Z" },
      ],
    })
    mockedAPI.shareLinks.mockResolvedValue({ links: [] })
    // jsdom's Blob lacks text(); browsers have it.
    const blob = Object.assign(new Blob(["# Hello Kervan"]), { text: async () => "# Hello Kervan" })
    mockedAPI.downloadFile.mockResolvedValue({ blob, filename: null })
    renderWithProviders(<FilesPage token="t" />)

    expect(await screen.findByText("1.5 KB")).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: "Preview archive.zip" })).toBeNull()
    expect(screen.getByRole("button", { name: "Download archive.zip" })).toBeInTheDocument()

    await user.click(screen.getByRole("button", { name: "Preview readme.md" }))
    expect(await screen.findByText("# Hello Kervan")).toBeInTheDocument()
    expect(mockedAPI.downloadFile).toHaveBeenCalledWith("t", "/readme.md")
  })
})
