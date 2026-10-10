import { afterEach, describe, expect, it, vi } from "vitest"

import { api } from "@/lib/api"
import { useAuthStore } from "@/stores/auth-store"

describe("auth store session handling", () => {
  afterEach(() => vi.unstubAllGlobals())

  it("returns to sign-in when the server rejects the session", async () => {
    useAuthStore.setState({ auth: { token: "revoked", user: { id: "1", username: "ann", type: "admin" } }, authError: null })
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: "invalid token" }), { status: 401 })))

    await expect(api.users("revoked")).rejects.toThrow("Session expired")
    expect(useAuthStore.getState().auth).toBeNull()
    expect(useAuthStore.getState().authError).toMatch(/session has ended/)
  })

  it("replaces the token without dropping the user", () => {
    useAuthStore.setState({ auth: { token: "a", user: { id: "1", username: "ann", type: "admin" } } })
    useAuthStore.getState().replaceToken("b")
    expect(useAuthStore.getState().auth).toEqual({ token: "b", user: { id: "1", username: "ann", type: "admin" } })
  })
})
