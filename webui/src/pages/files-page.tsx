import { useEffect, useMemo, useState } from "react"
import { FolderPlus, Upload } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiFileEntry } from "@/lib/types"

type Props = { token: string }

export function FilesPage({ token }: Props) {
  const [path, setPath] = useState("/")
  const [entries, setEntries] = useState<ApiFileEntry[]>([])
  const [error, setError] = useState<string | null>(null)
  const [folderName, setFolderName] = useState("")

  const load = async (targetPath = path) => {
    try {
      const data = await api.files(token, targetPath)
      setEntries(data.entries)
      setPath(data.path)
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
                    <Button variant="destructive" size="sm" onClick={() => void remove(entry)}>
                      Delete
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>
    </section>
  )
}



