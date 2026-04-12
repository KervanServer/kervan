import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"

export function useServerConfig(token: string) {
  return useQuery({
    queryKey: ["server-config", token],
    queryFn: () => api.serverConfig(token),
  })
}

export function useSaveServerConfig(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (patch: Record<string, unknown>) => api.updateServerConfig(token, patch),
    onSuccess: async () => {
      toast.success("Configuration saved.")
      await queryClient.invalidateQueries({ queryKey: ["server-config", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to update configuration")
    },
  })
}

export function useValidateServerConfig(token: string) {
  return useMutation({
    mutationFn: (patch: Record<string, unknown>) => api.validateServerConfig(token, patch),
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to validate configuration patch")
    },
  })
}

export function useReloadServerConfig(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: () => api.reloadServer(token),
    onSuccess: async () => {
      toast.success("Configuration reloaded.")
      await queryClient.invalidateQueries({ queryKey: ["server-config", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to reload configuration")
    },
  })
}
