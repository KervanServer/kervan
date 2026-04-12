import { screen } from "@testing-library/react"
import { ErrorBoundary } from "react-error-boundary"
import { afterEach, describe, expect, it, vi } from "vitest"

import { RouteErrorBoundary } from "@/components/shared/route-error-boundary"
import { renderWithProviders } from "@/test-utils"

function CrashingRoute() {
  throw new Error("Synthetic route failure")
  return null
}

describe("RouteErrorBoundary", () => {
  afterEach(() => {
    vi.restoreAllMocks()
  })

  it("renders a retry-oriented fallback for crashed routes", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {})

    renderWithProviders(
      <ErrorBoundary FallbackComponent={RouteErrorBoundary}>
        <CrashingRoute />
      </ErrorBoundary>,
    )

    expect(await screen.findByRole("heading", { name: "Something went wrong" })).toBeInTheDocument()
    expect(screen.getByText("Synthetic route failure")).toBeInTheDocument()
    expect(screen.getByRole("button", { name: "Retry" })).toBeInTheDocument()
  })
})
