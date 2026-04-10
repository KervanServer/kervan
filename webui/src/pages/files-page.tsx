import { useEffect, useMemo, useState } from "react"
import { Copy, FolderPlus, Pencil, Share2, Upload } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiFileEntry, ApiShareLink } from "@/lib/types"

type Props = { token: string }

export function FilesPage({ token }: Props) {
  const [path, setPath] = useState("/")
  const [entries, setEntries] = useState<ApiFileEntry[]>([])
  const [shareLinks, setShareLinks] = useState<ApiShareLink[]>([])
  const [error, setError] = useState<string | null>(null)
  const [folderName, setFolderName] = useState("")
  const [shareLink, setShareLink] = useState<string | null>(null)
  const [shareExpiresAt, setShareExpiresAt] = useState<string | null>(null)

  const load = async (targetPath = path) => {
    try {
      const [filesData, linksData] = await Promise.all([api.files(token, targetPath), api.shareLinks(token)])
      setEntries(filesData.entries)
      setPath(filesData.path)
      setShareLinks(linksData.links)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to browse files")
    }
  }

  useEffect(() => {
    void load("/")
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  const pathParts = useMemo(() => path.split("/").filter(Boolean), [path])

  const createFolder = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!folderName.trim()) {
      return
    }
    const target = `${path.replace(/\/$/, "")}/${folderName.trim()}`.replace(/\/+/g, "/")
    try {
      await api.mkdir(token, target)
      setFolderName("")
      await load(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to create folder")
    }
  }

  const upload = async (event: React.FormEvent<HTMLInputElement>) => {
    const file = event.currentTarget.files?.[0]
    if (!file) {
      return
    }
    const target = `${path.replace(/\/$/, "")}/${file.name}`.replace(/\/+/g, "/")
    try {
      await api.upload(token, target, file)
      await load(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to upload file")
    }
  }

  const remove = async (entry: ApiFileEntry) => {
    if (!window.confirm(`Delete ${entry.name}?`)) {
      return
    }
    try {
      await api.remove(token, entry.path, entry.is_dir)
      await load(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to delete entry")
    }
  }

  const share = async (entry: ApiFileEntry) => {
    if (entry.is_dir) {
      setError("Directory sharing is not supported yet")
      return
    }
    const ttl = window.prompt("Share TTL (e.g. 24h, 7d)", "24h")
    if (ttl === null) {
      return
    }
    try {
      const response = await api.createShareLink(token, entry.path, ttl.trim() || "24h")
      const full = response.share_url.startsWith("http") ? response.share_url : `${window.location.origin}${response.share_url}`
      setShareLink(full)
      setShareExpiresAt(response.expires_at)
      await load(path)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to create share link")
    }
  }

  const copyShareLink = async () => {
    if (!shareLink) {
      return
    }
    try {
      await navigator.clipboard.writeText(shareLink)
    } catch {
      // no-op
    }
  }

  const revokeShareLink = async (shareToken: string) => {
    if (!window.confirm("Revoke this share link?")) {
      return
    }
    try {
      await api.revokeShareLink(token, shareToken)
      await load(path)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to revoke share link")
    }
  }

  const rename = async (entry: ApiFileEntry) => {
    const nextName = window.prompt("New name", entry.name)
    if (nextName === null) {
      return
    }
    const trimmed = nextName.trim()
    if (!trimmed || trimmed === entry.name) {
      return
    }
    const slash = entry.path.lastIndexOf("/")
    const parent = slash > 0 ? entry.path.slice(0, slash) : "/"
    const target = `${parent}/${trimmed}`.replace(/\/+/g, "/")
    try {
      await api.rename(token, entry.path, target)
      await load(path)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to rename entry")
    }
  }

  const navigate = async (index: number) => {
    if (index < 0) {
      await load("/")
      return
    }
    const next = `/${pathParts.slice(0, index + 1).join("/")}`
    await load(next)
  }

  return (
    <section className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>File Browser</CardTitle>
          <CardDescription>Responsive VFS browser for the authenticated user.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 rounded-xl bg-[var(--muted)] p-2 text-sm">
            <Button size="sm" variant="ghost" onClick={() => void navigate(-1)}>
              /
            </Button>
            {pathParts.map((part, index) => (
              <Button key={`${part}-${index}`} size="sm" variant="ghost" onClick={() => void navigate(index)}>
                {part}
              </Button>
            ))}
          </div>

          <div className="grid gap-2 md:grid-cols-[1fr_auto_auto]">
            <form className="flex gap-2" onSubmit={createFolder}>
              <Input placeholder="New folder" value={folderName} onChange={(e) => setFolderName(e.target.value)} />
              <Button type="submit" variant="secondary">
                <FolderPlus className="mr-2 h-4 w-4" />
                Create
              </Button>
            </form>
            <label className="inline-flex cursor-pointer items-center justify-center rounded-xl border border-[var(--border)] bg-[var(--card)] px-3 text-sm hover:bg-[var(--muted)]">
              <Upload className="mr-2 h-4 w-4" />
              Upload
              <input type="file" className="hidden" onChange={upload} />
            </label>
            <Button variant="outline" onClick={() => void load(path)}>
              Refresh
            </Button>
          </div>

          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
          {shareLink ? (
            <div className="rounded-xl border border-[var(--border)] bg-[var(--muted)] p-3">
              <p className="text-sm font-medium">Share link created</p>
              <p className="mt-1 break-all text-xs">{shareLink}</p>
              <div className="mt-2 flex items-center gap-2">
                <Button size="sm" variant="outline" onClick={() => void copyShareLink()}>
                  <Copy className="mr-2 h-3.5 w-3.5" />
                  Copy
                </Button>
                {shareExpiresAt ? (
                  <span className="text-xs text-[var(--muted-foreground)]">Expires: {new Date(shareExpiresAt).toLocaleString()}</span>
                ) : null}
              </div>
            </div>
          ) : null}

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
                      <button className="text-left underline-offset-4 hover:underline" onClick={() => void load(entry.path)}>
                        {entry.name}
                      </button>
                    ) : (
                      entry.name
                    )}
                  </TableCell>
                  <TableCell>{entry.is_dir ? "Directory" : "File"}</TableCell>
                  <TableCell>{entry.size}</TableCell>
                  <TableCell className="text-right">
                    <div className="flex justify-end gap-2">
                      {!entry.is_dir ? (
                        <Button variant="outline" size="sm" onClick={() => void share(entry)}>
                          <Share2 className="mr-2 h-3.5 w-3.5" />
                          Share
                        </Button>
                      ) : null}
                      <Button variant="outline" size="sm" onClick={() => void rename(entry)}>
                        <Pencil className="mr-2 h-3.5 w-3.5" />
                        Rename
                      </Button>
                      <Button variant="destructive" size="sm" onClick={() => void remove(entry)}>
                        Delete
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          <div className="space-y-2 rounded-xl border border-[var(--border)] p-3">
            <h3 className="text-sm font-medium">Share Links</h3>
            {shareLinks.length === 0 ? (
              <p className="text-xs text-[var(--muted-foreground)]">No active links yet.</p>
            ) : (
              <div className="space-y-2">
                {shareLinks.map((link) => {
                  const full = link.share_url.startsWith("http") ? link.share_url : `${window.location.origin}${link.share_url}`
                  return (
                    <div key={link.token} className="rounded-lg border border-[var(--border)] bg-[var(--muted)] p-2 text-xs">
                      <p className="font-medium">{link.path}</p>
                      <p className="mt-1 break-all">{full}</p>
                      <div className="mt-2 flex flex-wrap items-center gap-2">
                        <Button size="sm" variant="outline" onClick={() => void navigator.clipboard.writeText(full)}>
                          <Copy className="mr-2 h-3.5 w-3.5" />
                          Copy
                        </Button>
                        <Button size="sm" variant="destructive" onClick={() => void revokeShareLink(link.token)}>
                          Revoke
                        </Button>
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
    </section>
  )
}



