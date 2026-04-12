import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"

export function useAPIKeys(token: string) {
  return useQuery({
    queryKey: ["api-keys", token],
    queryFn: () => api.apiKeys(token),
  })
}

export function useCreateAPIKey(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: { name: string; permissions: string }) => api.createApiKey(token, payload),
    onSuccess: async () => {
      toast.success("API key created successfully.")
      await queryClient.invalidateQueries({ queryKey: ["api-keys", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to create API key")
    },
  })
}

export function useDeleteAPIKey(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => api.deleteApiKey(token, id),
    onSuccess: async () => {
      toast.success("API key revoked.")
      await queryClient.invalidateQueries({ queryKey: ["api-keys", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to revoke API key")
    },
  })
}
