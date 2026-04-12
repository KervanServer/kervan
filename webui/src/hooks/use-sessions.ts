import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"

export function useSessions(token: string) {
  return useQuery({
    queryKey: ["sessions", token],
    queryFn: () => api.sessions(token),
  })
}

export function useKillSession(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => api.killSession(token, id),
    onSuccess: async () => {
      toast.success("Session disconnected.")
      await queryClient.invalidateQueries({ queryKey: ["sessions", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to disconnect session")
    },
  })
}
