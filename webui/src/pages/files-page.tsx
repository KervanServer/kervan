import { useEffect, useMemo, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Copy, FileText, FolderPlus, Loader2, Pencil, RefreshCw, Share2, Upload } from "lucide-react"
import { useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod"

import { ConfirmDialog } from "@/components/shared/confirm-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import {
  useCreateFolder,
  useCreateShareLink,
  useDeleteFile,
  useFiles,
  useRenameFile,
  useRevokeShareLink,
  useShareLinks,
  useUploadFile,
} from "@/hooks/use-files"
import type { ApiFileEntry, ApiShareLink } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip } from "@/components/ui/tooltip"

type Props = { token: string }

const folderSchema = z.object({
  name: z.string().trim().min(1, "Folder name is required").max(128, "Folder name is too long"),
})

const renameSchema = z.object({
  name: z.string().trim().min(1, "Name is required").max(128, "Name is too long"),
})

const shareSchema = z.object({
  ttl: z.string().trim().min(1, "TTL is required").max(32, "TTL is too long"),
})

type FolderValues = z.infer<typeof folderSchema>
type RenameValues = z.infer<typeof renameSchema>
type ShareValues = z.infer<typeof shareSchema>

type SharePreview = {
  url: string
  expiresAt: string
}

function joinPath(basePath: string, child: string): string {
  return `${basePath.replace(/\/$/, "")}/${child.trim()}`.replace(/\/+/g, "/")
}

function parentPath(path: string): string {
  const slash = path.lastIndexOf("/")
  if (slash <= 0) {
    return "/"
  }
  return path.slice(0, slash)
}

function toAbsoluteShareURL(shareURL: string): string {
  return shareURL.startsWith("http") ? shareURL : `${window.location.origin}${shareURL}`
}

export function FilesPage({ token }: Props) {
  const [path, setPath] = useState("/")
  const [sharePreview, setSharePreview] = useState<SharePreview | null>(null)
  const [entryToDelete, setEntryToDelete] = useState<ApiFileEntry | null>(null)
  const [entryToRename, setEntryToRename] = useState<ApiFileEntry | null>(null)
  const [entryToShare, setEntryToShare] = useState<ApiFileEntry | null>(null)
  const [shareLinkToRevoke, setShareLinkToRevoke] = useState<ApiShareLink | null>(null)

  const filesQuery = useFiles(token, path)
  const shareLinksQuery = useShareLinks(token)
  const createFolderMutation = useCreateFolder(token)
  const uploadFileMutation = useUploadFile(token)
  const deleteFileMutation = useDeleteFile(token)
  const renameFileMutation = useRenameFile(token)
  const createShareLinkMutation = useCreateShareLink(token)
  const revokeShareLinkMutation = useRevokeShareLink(token)

  const {
    register: registerFolder,
    handleSubmit: handleCreateFolderSubmit,
    reset: resetFolderForm,
    formState: { errors: folderErrors, isValid: isFolderValid },
  } = useForm<FolderValues>({
    resolver: zodResolver(folderSchema),
    mode: "onChange",
    defaultValues: { name: "" },
  })

  const {
    register: registerRename,
    handleSubmit: handleRenameSubmit,
    reset: resetRenameForm,
    formState: { errors: renameErrors, isValid: isRenameValid },
  } = useForm<RenameValues>({
    resolver: zodResolver(renameSchema),
    mode: "onChange",
    defaultValues: { name: "" },
  })

  const {
    register: registerShare,
    handleSubmit: handleShareSubmit,
    reset: resetShareForm,
    formState: { errors: shareErrors, isValid: isShareValid },
  } = useForm<ShareValues>({
    resolver: zodResolver(shareSchema),
    mode: "onChange",
    defaultValues: { ttl: "24h" },
  })

  useEffect(() => {
    if (filesQuery.data?.path && filesQuery.data.path !== path) {
      setPath(filesQuery.data.path)
    }
  }, [filesQuery.data?.path, path])

  useEffect(() => {
    if (entryToRename) {
      resetRenameForm({ name: entryToRename.name })
    }
  }, [entryToRename, resetRenameForm])

  useEffect(() => {
    if (entryToShare) {
      resetShareForm({ ttl: "24h" })
    }
  }, [entryToShare, resetShareForm])

  const entries = filesQuery.data?.entries ?? []
  const shareLinks = shareLinksQuery.data?.links ?? []
  const pathParts = useMemo(() => path.split("/").filter(Boolean), [path])
  const error =
    (filesQuery.error instanceof Error ? filesQuery.error.message : null) ??
    (shareLinksQuery.error instanceof Error ? shareLinksQuery.error.message : null)

  const onNavigate = (nextPath: string) => {
    setPath(nextPath)
    setSharePreview(null)
  }

  const onCreateFolder = async (values: FolderValues) => {
    await createFolderMutation.mutateAsync(joinPath(path, values.name))
    resetFolderForm({ name: "" })
  }

  const onUpload = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0]
    if (!file) {
      return
    }
    await uploadFileMutation.mutateAsync({ targetPath: joinPath(path, file.name), file })
    event.target.value = ""
  }

  const onConfirmDelete = async () => {
    if (!entryToDelete) {
      return
    }
    await deleteFileMutation.mutateAsync({
      targetPath: entryToDelete.path,
      recursive: entryToDelete.is_dir,
    })
    setEntryToDelete(null)
  }

  const onRename = async (values: RenameValues) => {
    if (!entryToRename) {
      return
    }
    const targetPath = joinPath(parentPath(entryToRename.path), values.name)
    if (targetPath === entryToRename.path) {
      setEntryToRename(null)
      return
    }
    await renameFileMutation.mutateAsync({
      fromPath: entryToRename.path,
      toPath: targetPath,
    })
    setEntryToRename(null)
  }

  const onShare = async (values: ShareValues) => {
    if (!entryToShare) {
      return
    }
    const response = await createShareLinkMutation.mutateAsync({
      targetPath: entryToShare.path,
      ttl: values.ttl.trim(),
    })
    setSharePreview({
      url: toAbsoluteShareURL(response.share_url),
      expiresAt: response.expires_at,
    })
    setEntryToShare(null)
  }

  const onConfirmRevoke = async () => {
    if (!shareLinkToRevoke) {
      return
    }
    await revokeShareLinkMutation.mutateAsync(shareLinkToRevoke.token)
    setShareLinkToRevoke(null)
  }

  const copyToClipboard = async (value: string, successMessage: string) => {
    try {
      await navigator.clipboard.writeText(value)
      toast.success(successMessage)
    } catch {
      toast.error("Unable to copy to clipboard")
    }
  }

  return (
    <section className="space-y-4">
      <PageHeader
        title="Files"
        description="Browse virtual storage, create folders, upload files, and manage secure share links."
        actions={
          <Tooltip content="Refresh current folder">
            <Button
              variant="outline"
              onClick={() => void filesQuery.refetch()}
              disabled={filesQuery.isFetching}
              aria-label="Refresh current folder"
            >
              <RefreshCw className={`mr-2 h-4 w-4 ${filesQuery.isFetching ? "animate-spin motion-reduce:animate-none" : ""}`} />
              {filesQuery.isFetching ? "Refreshing..." : "Refresh"}
            </Button>
          </Tooltip>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>File Browser</CardTitle>
          <CardDescription>Responsive VFS browser for the authenticated user.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex flex-wrap items-center gap-2 rounded-xl bg-[var(--muted)] p-2 text-sm">
            <Button size="sm" variant="ghost" onClick={() => onNavigate("/")} aria-label="Go to root directory">
              /
            </Button>
            {pathParts.map((part, index) => (
              <Button
                key={`${part}-${index}`}
                size="sm"
                variant="ghost"
                onClick={() => onNavigate(`/${pathParts.slice(0, index + 1).join("/")}`)}
              >
                {part}
              </Button>
            ))}
          </div>

          <div className="grid gap-3 lg:grid-cols-[1fr_auto]">
            <form className="space-y-2" onSubmit={handleCreateFolderSubmit(onCreateFolder)}>
              <div className="flex gap-2">
                <Input
                  placeholder="New folder"
                  aria-invalid={folderErrors.name ? "true" : "false"}
                  {...registerFolder("name")}
                />
                <Button type="submit" variant="secondary" disabled={createFolderMutation.isPending || !isFolderValid}>
                  {createFolderMutation.isPending ? (
                    <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                  ) : (
                    <FolderPlus className="mr-2 h-4 w-4" />
                  )}
                  {createFolderMutation.isPending ? "Creating..." : "Create"}
                </Button>
              </div>
              {folderErrors.name ? <p className="text-sm text-[var(--error)]">{folderErrors.name.message}</p> : null}
            </form>

            <label className="inline-flex min-h-11 cursor-pointer items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--card)] px-3 text-sm transition hover:bg-[var(--muted)]">
              <Upload className="mr-2 h-4 w-4" />
              {uploadFileMutation.isPending ? "Uploading..." : "Upload"}
              <input
                type="file"
                className="hidden"
                onChange={onUpload}
                disabled={uploadFileMutation.isPending}
                aria-label="Upload file"
              />
            </label>
          </div>

          {error ? <StatusMessage variant="error">{error}</StatusMessage> : null}

          {sharePreview ? (
            <div className="rounded-xl border border-[var(--border)] bg-[var(--muted)] p-3" role="status" aria-live="polite">
              <p className="text-sm font-medium">Share link created</p>
              <p className="mt-1 break-all text-xs">{sharePreview.url}</p>
              <div className="mt-2 flex flex-wrap items-center gap-2">
                <Tooltip content="Copy the new share link">
                  <Button
                    size="sm"
                    variant="outline"
                    onClick={() => void copyToClipboard(sharePreview.url, "Share link copied.")}
                    aria-label="Copy new share link"
                  >
                    <Copy className="mr-2 h-3.5 w-3.5" />
                    Copy
                  </Button>
                </Tooltip>
                <span className="text-xs text-[var(--muted-foreground)]">
                  Expires: {new Date(sharePreview.expiresAt).toLocaleString()}
                </span>
              </div>
            </div>
          ) : null}

          {filesQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 5 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : entries.length === 0 ? (
            <EmptyState
              title="No files in this folder"
              description="Create a folder or upload a file to start managing content in this location."
              icon={FileText}
            />
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)]">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Size</TableHead>
                    <TableHead className="text-right">Action</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {entries.map((entry) => (
                    <TableRow key={entry.path}>
                      <TableCell>
                        {entry.is_dir ? (
                          <Button
                            variant="ghost"
                            className="h-auto p-0 text-left underline-offset-4 hover:underline"
                            onClick={() => onNavigate(entry.path)}
                          >
                            {entry.name}
                          </Button>
                        ) : (
                          entry.name
                        )}
                      </TableCell>
                      <TableCell>{entry.is_dir ? "Directory" : "File"}</TableCell>
                      <TableCell>{entry.size}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          {!entry.is_dir ? (
                            <Tooltip content={`Share ${entry.name}`}>
                              <Button
                                variant="outline"
                                size="sm"
                                onClick={() => setEntryToShare(entry)}
                                aria-label={`Create share link for ${entry.name}`}
                              >
                                <Share2 className="mr-2 h-3.5 w-3.5" />
                                Share
                              </Button>
                            </Tooltip>
                          ) : null}
                          <Tooltip content={`Rename ${entry.name}`}>
                            <Button
                              variant="outline"
                              size="sm"
                              onClick={() => setEntryToRename(entry)}
                              aria-label={`Rename ${entry.name}`}
                            >
                              <Pencil className="mr-2 h-3.5 w-3.5" />
                              Rename
                            </Button>
                          </Tooltip>
                          <Tooltip content={`Delete ${entry.name}`}>
                            <Button
                              variant="destructive"
                              size="sm"
                              onClick={() => setEntryToDelete(entry)}
                              aria-label={`Delete ${entry.name}`}
                            >
                              Delete
                            </Button>
                          </Tooltip>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}

          <div className="space-y-3 rounded-xl border border-[var(--border)] p-3">
            <h3 className="text-sm font-medium">Share Links</h3>
            {shareLinksQuery.isLoading ? (
              <div className="space-y-2">
                {Array.from({ length: 3 }).map((_, index) => (
                  <Skeleton key={index} className="h-20 w-full" />
                ))}
              </div>
            ) : shareLinks.length === 0 ? (
              <EmptyState
                title="No active share links"
                description="Create a share link for any file to distribute it securely."
                icon={Share2}
              />
            ) : (
              <div className="space-y-2">
                {shareLinks.map((link) => {
                  const full = toAbsoluteShareURL(link.share_url)
                  return (
                    <div key={link.token} className="rounded-lg border border-[var(--border)] bg-[var(--muted)] p-3 text-xs">
                      <p className="font-medium">{link.path}</p>
                      <p className="mt-1 break-all">{full}</p>
                      <div className="mt-2 flex flex-wrap items-center gap-2">
                        <Tooltip content="Copy share link">
                          <Button
                            size="sm"
                            variant="outline"
                            onClick={() => void copyToClipboard(full, "Share link copied.")}
                            aria-label={`Copy share link for ${link.path}`}
                          >
                            <Copy className="mr-2 h-3.5 w-3.5" />
                            Copy
                          </Button>
                        </Tooltip>
                        <Tooltip content={`Revoke link for ${link.path}`}>
                          <Button
                            size="sm"
                            variant="destructive"
                            onClick={() => setShareLinkToRevoke(link)}
                            aria-label={`Revoke share link for ${link.path}`}
                          >
                            Revoke
                          </Button>
                        </Tooltip>
                        <span className="text-[var(--muted-foreground)]">
                          Expires: {new Date(link.expires_at).toLocaleString()}
                        </span>
                        <span className="text-[var(--muted-foreground)]">
                          Downloads: {link.download_count}
                          {link.max_downloads > 0 ? `/${link.max_downloads}` : ""}
                        </span>
                      </div>
                    </div>
                  )
                })}
              </div>
            )}
          </div>
        </CardContent>
      </Card>

      <Dialog open={entryToRename !== null} onOpenChange={(open) => (!open ? setEntryToRename(null) : null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Rename entry</DialogTitle>
            <DialogDescription>
              {entryToRename ? `Choose a new name for "${entryToRename.name}".` : "Choose a new name."}
            </DialogDescription>
          </DialogHeader>
          <form className="space-y-4" onSubmit={handleRenameSubmit(onRename)}>
            <div className="space-y-2">
              <Input
                placeholder="New name"
                aria-invalid={renameErrors.name ? "true" : "false"}
                {...registerRename("name")}
              />
              {renameErrors.name ? <p className="text-sm text-[var(--error)]">{renameErrors.name.message}</p> : null}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEntryToRename(null)}>
                Cancel
              </Button>
              <Button type="submit" disabled={renameFileMutation.isPending || !isRenameValid}>
                {renameFileMutation.isPending ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                ) : null}
                {renameFileMutation.isPending ? "Saving..." : "Rename"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={entryToShare !== null} onOpenChange={(open) => (!open ? setEntryToShare(null) : null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Create share link</DialogTitle>
            <DialogDescription>
              {entryToShare ? `Create a temporary share link for "${entryToShare.name}".` : "Create a temporary share link."}
            </DialogDescription>
          </DialogHeader>
          <form className="space-y-4" onSubmit={handleShareSubmit(onShare)}>
            <div className="space-y-2">
              <Input placeholder="TTL (e.g. 24h, 7d)" aria-invalid={shareErrors.ttl ? "true" : "false"} {...registerShare("ttl")} />
              {shareErrors.ttl ? <p className="text-sm text-[var(--error)]">{shareErrors.ttl.message}</p> : null}
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setEntryToShare(null)}>
                Cancel
              </Button>
              <Button type="submit" disabled={createShareLinkMutation.isPending || !isShareValid}>
                {createShareLinkMutation.isPending ? (
                  <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
                ) : null}
                {createShareLinkMutation.isPending ? "Creating..." : "Create link"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={entryToDelete !== null}
        title="Delete entry"
        description={entryToDelete ? `Delete "${entryToDelete.name}"? This action cannot be undone.` : ""}
        confirmLabel="Delete entry"
        pending={deleteFileMutation.isPending}
        onConfirm={() => void onConfirmDelete()}
        onOpenChange={(open) => {
          if (!open) {
            setEntryToDelete(null)
          }
        }}
      />

      <ConfirmDialog
        open={shareLinkToRevoke !== null}
        title="Revoke share link"
        description={shareLinkToRevoke ? `Revoke the share link for "${shareLinkToRevoke.path}"? Existing URLs will stop working immediately.` : ""}
        confirmLabel="Revoke link"
        pending={revokeShareLinkMutation.isPending}
        onConfirm={() => void onConfirmRevoke()}
        onOpenChange={(open) => {
          if (!open) {
            setShareLinkToRevoke(null)
          }
        }}
      />
    </section>
  )
}
