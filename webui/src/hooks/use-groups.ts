import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"
import type { ApiPermissions } from "@/lib/types"

export function useGroups(token: string) {
  return useQuery({
    queryKey: ["groups", token],
    queryFn: () => api.groups(token),
  })
}

function useInvalidateGroups(token: string) {
  const queryClient = useQueryClient()
  return async () => {
    await queryClient.invalidateQueries({ queryKey: ["groups", token] })
    // Effective user policies depend on group templates.
    await queryClient.invalidateQueries({ queryKey: ["users", token] })
  }
}

export function useCreateGroup(token: string) {
  const invalidate = useInvalidateGroups(token)
  return useMutation({
    mutationFn: (payload: { name: string; description?: string; permissions: ApiPermissions; max_storage: number; max_bandwidth: number; max_files: number }) =>
      api.createGroup(token, payload),
    onSuccess: async () => {
      toast.success("Group created.")
      await invalidate()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to create group")
    },
  })
}

export function useUpdateGroup(token: string) {
  const invalidate = useInvalidateGroups(token)
  return useMutation({
    mutationFn: (payload: {
      id: string
      name?: string
      description?: string
      permissions?: ApiPermissions
      max_storage?: number
      max_bandwidth?: number
      max_files?: number
    }) =>
      api.updateGroup(token, payload),
    onSuccess: async () => {
      toast.success("Group updated.")
      await invalidate()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to update group")
    },
  })
}

export function useDeleteGroup(token: string) {
  const invalidate = useInvalidateGroups(token)
  return useMutation({
    mutationFn: ({ id, force }: { id: string; force?: boolean }) => api.deleteGroup(token, id, force),
    onSuccess: async () => {
      toast.success("Group deleted.")
      await invalidate()
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to delete group")
    },
  })
}
