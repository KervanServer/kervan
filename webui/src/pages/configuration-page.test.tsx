import { fireEvent, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { beforeEach, describe, expect, it, vi } from "vitest"

import { ConfigurationPage } from "@/pages/configuration-page"
import { api } from "@/lib/api"
import { renderWithProviders } from "@/test-utils"

vi.mock("@/lib/api", () => ({
  api: {
    serverConfig: vi.fn(),
    updateServerConfig: vi.fn(),
    validateServerConfig: vi.fn(),
    reloadServer: vi.fn(),
  },
}))

const mockedAPI = vi.mocked(api)

const baseConfig = {
  server: {
    data_dir: "./data",
    log_level: "info",
  },
  auth: {
    min_password_length: 12,
  },
}

describe("ConfigurationPage", () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mockedAPI.serverConfig.mockResolvedValue({ config: baseConfig })
    mockedAPI.updateServerConfig.mockResolvedValue({ updated: true, changed_paths: ["server.log_level"] })
    mockedAPI.validateServerConfig.mockResolvedValue({ validated: true, changed_paths: ["server.log_level"] })
    mockedAPI.reloadServer.mockResolvedValue({ reloaded: true })
  })

  it("validates only the changed config paths", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ConfigurationPage token="token-123" />)

    const editor = await screen.findByRole("textbox")
    fireEvent.change(editor, {
      target: {
        value: JSON.stringify(
          {
            server: {
              data_dir: "./data",
              log_level: "debug",
            },
            auth: {
              min_password_length: 12,
            },
          },
          null,
          2,
        ),
      },
    })
    await user.click(screen.getByRole("button", { name: "Validate Patch" }))

    await waitFor(() =>
      expect(mockedAPI.validateServerConfig).toHaveBeenCalledWith("token-123", {
        server: {
          log_level: "debug",
        },
      }),
    )
    expect(await screen.findByText("Validation Result")).toBeInTheDocument()
  })

  it("reports when there are no config changes to save", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ConfigurationPage token="token-123" />)

    await screen.findByRole("textbox")
    await user.click(screen.getByRole("button", { name: "Save Config" }))

    await waitFor(() => expect(mockedAPI.updateServerConfig).not.toHaveBeenCalled())
    expect(await screen.findByText("Save Result")).toBeInTheDocument()
    expect(screen.getByText(/No changes detected\./)).toBeInTheDocument()
  })

  it("reloads the server config and refreshes the snapshot", async () => {
    const user = userEvent.setup()
    renderWithProviders(<ConfigurationPage token="token-123" />)

    await screen.findByRole("textbox")
    await user.click(screen.getByRole("button", { name: "Reload Config" }))

    await waitFor(() => expect(mockedAPI.reloadServer).toHaveBeenCalledWith("token-123"))
    expect(await screen.findByText("Reload Result")).toBeInTheDocument()
    expect(mockedAPI.serverConfig).toHaveBeenCalledTimes(2)
  })
})
