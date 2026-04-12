import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"
import type { ApiUserImportReport } from "@/lib/types"

export function useUsers(token: string) {
  return useQuery({
    queryKey: ["users", token],
    queryFn: () => api.users(token),
  })
}

export function useCreateUser(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: { username: string; password: string; home_dir: string; admin: boolean }) =>
      api.createUser(token, payload),
    onSuccess: async () => {
      toast.success("User created successfully.")
      await queryClient.invalidateQueries({ queryKey: ["users", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to create user")
    },
  })
}

export function useUpdateUser(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (payload: { id: string; enabled?: boolean; home_dir?: string; admin?: boolean }) =>
      api.updateUser(token, payload),
    onSuccess: async () => {
      toast.success("User updated.")
      await queryClient.invalidateQueries({ queryKey: ["users", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to update user")
    },
  })
}

export function useDeleteUser(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (id: string) => api.deleteUser(token, id),
    onSuccess: async () => {
      toast.success("User deleted.")
      await queryClient.invalidateQueries({ queryKey: ["users", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to delete user")
    },
  })
}

export function useImportUsers(token: string, onSuccess?: (report: ApiUserImportReport) => void) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (file: File) => api.importUsers(token, file),
    onSuccess: async (report) => {
      toast.success("Users imported.")
      onSuccess?.(report)
      await queryClient.invalidateQueries({ queryKey: ["users", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to import users")
    },
  })
}
