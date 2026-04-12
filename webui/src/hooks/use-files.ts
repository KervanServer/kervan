import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { toast } from "sonner"

import { api } from "@/lib/api"

export function useFiles(token: string, path: string) {
  return useQuery({
    queryKey: ["files", token, path],
    queryFn: () => api.files(token, path),
  })
}

export function useShareLinks(token: string) {
  return useQuery({
    queryKey: ["share-links", token],
    queryFn: () => api.shareLinks(token),
  })
}

export function useCreateFolder(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (targetPath: string) => api.mkdir(token, targetPath),
    onSuccess: async () => {
      toast.success("Folder created.")
      await queryClient.invalidateQueries({ queryKey: ["files", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to create folder")
    },
  })
}

export function useUploadFile(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ targetPath, file }: { targetPath: string; file: File }) => api.upload(token, targetPath, file),
    onSuccess: async () => {
      toast.success("File uploaded.")
      await queryClient.invalidateQueries({ queryKey: ["files", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to upload file")
    },
  })
}

export function useDeleteFile(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ targetPath, recursive }: { targetPath: string; recursive?: boolean }) =>
      api.remove(token, targetPath, recursive),
    onSuccess: async () => {
      toast.success("Entry deleted.")
      await queryClient.invalidateQueries({ queryKey: ["files", token] })
      await queryClient.invalidateQueries({ queryKey: ["share-links", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to delete entry")
    },
  })
}

export function useRenameFile(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ fromPath, toPath }: { fromPath: string; toPath: string }) => api.rename(token, fromPath, toPath),
    onSuccess: async () => {
      toast.success("Entry renamed.")
      await queryClient.invalidateQueries({ queryKey: ["files", token] })
      await queryClient.invalidateQueries({ queryKey: ["share-links", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to rename entry")
    },
  })
}

export function useCreateShareLink(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: ({ targetPath, ttl }: { targetPath: string; ttl: string }) => api.createShareLink(token, targetPath, ttl),
    onSuccess: async () => {
      toast.success("Share link created.")
      await queryClient.invalidateQueries({ queryKey: ["share-links", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to create share link")
    },
  })
}

export function useRevokeShareLink(token: string) {
  const queryClient = useQueryClient()

  return useMutation({
    mutationFn: (shareToken: string) => api.revokeShareLink(token, shareToken),
    onSuccess: async () => {
      toast.success("Share link revoked.")
      await queryClient.invalidateQueries({ queryKey: ["share-links", token] })
    },
    onError: (error) => {
      toast.error(error instanceof Error ? error.message : "Unable to revoke share link")
    },
  })
}
