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
  logout: () => void
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
  logout: () => {
    set({
      auth: null,
      authError: null,
      authLoading: false,
      requiresOTP: false,
    })
  },
}))
