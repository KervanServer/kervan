import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"

export function useDashboardStatus(token: string) {
  return useQuery({
    queryKey: ["server-status", token],
    queryFn: () => api.status(token),
  })
}

export function useTOTPStatus(token: string) {
  return useQuery({
    queryKey: ["totp-status", token],
    queryFn: () => api.totpStatus(token),
  })
}

export function usePrepareTOTPSetup(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => api.totpSetup(token),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ["totp-status", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to prepare two-factor setup")
    },
  })
}

export function useEnableTOTP(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (code: string) => api.totpEnable(token, code),
    onSuccess: async () => {
      toast.success("Two-factor authentication enabled.")
      await queryClient.invalidateQueries({ queryKey: ["totp-status", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to enable two-factor authentication")
    },
  })
}

export function useDisableTOTP(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (code: string) => api.totpDisable(token, code),
    onSuccess: async () => {
      toast.success("Two-factor authentication disabled.")
      await queryClient.invalidateQueries({ queryKey: ["totp-status", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to disable two-factor authentication")
    },
  })
}
