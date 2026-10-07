import { create } from "zustand"

import { api, RequestError } from "@/lib/api"
import type { AuthUser } from "@/lib/types"

type AuthState = {
  token: string
  user: AuthUser
}

type AuthStore = {
  auth: AuthState | null
  authError: string | null
  authLoading: boolean
  requiresOTP: boolean
  login: (username: string, password: string, otp?: string) => Promise<void>
  completeOIDC: (code: string) => Promise<void>
  setAuthError: (message: string | null) => void
  logout: () => void
}

const OIDC_ERRORS: Record<string, string> = {
  access_denied: "Sign-in was cancelled at the identity provider.",
  invalid_state: "The sign-in attempt expired or was started in another browser. Please try again.",
  token_error: "The identity provider's response could not be verified.",
  provider_unavailable: "The identity provider is unreachable. Try again or use a password.",
  missing_username: "The identity provider did not send a usable username.",
  not_allowed: "Your account is not in a group that may use Kervan.",
  not_provisioned: "No Kervan account exists for you yet. Ask an administrator to create one.",
  account_conflict: "This username already belongs to a local Kervan account.",
  account_disabled: "Your Kervan account is disabled.",
}

export function describeOIDCError(code: string): string {
  return OIDC_ERRORS[code] ?? `Single sign-on failed (${code}).`
}

export const useAuthStore = create<AuthStore>((set) => ({
  auth: null,
  authError: null,
  authLoading: false,
  requiresOTP: false,
  login: async (username: string, password: string, otp?: string) => {
    set({ authLoading: true })
    try {
      const result = await api.login(username, password, otp)
      set({
        auth: result,
        authError: null,
        authLoading: false,
        requiresOTP: false,
      })
    } catch (error) {
      set({
        authError: error instanceof Error ? error.message : "Login failed",
        authLoading: false,
        requiresOTP: error instanceof RequestError && error.code === "totp_required",
      })
    }
  },
  completeOIDC: async (code: string) => {
    set({ authLoading: true, authError: null })
    try {
      const result = await api.oidcExchange(code)
      set({ auth: result, authError: null, authLoading: false, requiresOTP: false })
    } catch (error) {
      set({
        authError: error instanceof Error ? error.message : "Single sign-on failed",
        authLoading: false,
      })
    }
  },
  setAuthError: (message: string | null) => {
    set({ authError: message })
  },
  logout: () => {
    set({
      auth: null,
      authError: null,
      authLoading: false,
      requiresOTP: false,
    })
  },
}))
