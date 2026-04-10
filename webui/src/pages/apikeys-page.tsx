import { useEffect, useState } from "react"
import { Copy, KeyRound, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiKey } from "@/lib/types"

type Props = { token: string }

export function ApiKeysPage({ token }: Props) {
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [name, setName] = useState("")
  const [permissions, setPermissions] = useState("read-write")
  const [generatedKey, setGeneratedKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const response = await api.apiKeys(token)
      setKeys(response.keys)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load API keys")
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  const create = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!name.trim()) {
      return
    }
    setCreating(true)
    try {
      const response = await api.createApiKey(token, { name: name.trim(), permissions })
      setGeneratedKey(response.key)
      setName("")
      await load()
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to create API key")
    } finally {
      setCreating(false)
    }
  }

  const revoke = async (id: string) => {
    if (!window.confirm("Revoke this API key?")) {
      return
    }
    try {
      await api.deleteApiKey(token, id)
      await load()
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to revoke API key")
    }
  }

  const copyGeneratedKey = async () => {
    if (!generatedKey) {
      return
    }
    try {
      await navigator.clipboard.writeText(generatedKey)
    } catch {
      // no-op
    }
  }

  return (
    <section className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>API Keys</CardTitle>
          <CardDescription>Create and revoke personal API keys.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <form className="grid gap-2 md:grid-cols-[1fr_auto_auto]" onSubmit={create}>
            <Input placeholder="Key name" value={name} onChange={(event) => setName(event.target.value)} />
            <div className="flex gap-1 rounded-xl border border-[var(--border)] bg-[var(--muted)] p-1">
              <Button
                type="button"
                size="sm"
                variant={permissions === "read-only" ? "default" : "ghost"}
                onClick={() => setPermissions("read-only")}
              >
                Read only
              </Button>
              <Button
                type="button"
                size="sm"
                variant={permissions === "read-write" ? "default" : "ghost"}
                onClick={() => setPermissions("read-write")}
              >
                Read/write
              </Button>
            </div>
            <Button type="submit" disabled={creating}>
              <KeyRound className="mr-2 h-4 w-4" />
              {creating ? "Creating..." : "Create key"}
            </Button>
          </form>

          {generatedKey ? (
            <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 p-3">
              <p className="text-sm font-medium">New API key (shown once)</p>
              <p className="mt-1 break-all font-mono text-xs">{generatedKey}</p>
              <div className="mt-2">
                <Button size="sm" variant="outline" onClick={() => void copyGeneratedKey()}>
                  <Copy className="mr-2 h-3.5 w-3.5" />
                  Copy
                </Button>
              </div>
            </div>
          ) : null}

          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}

          <div className="rounded-xl border border-[var(--border)]">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Permission</TableHead>
                  <TableHead>Prefix</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((key) => (
                  <TableRow key={key.id}>
                    <TableCell>{key.name}</TableCell>
                    <TableCell>
                      <Badge variant="outline">{key.permissions}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{key.prefix}</TableCell>
                    <TableCell>{new Date(key.created_at).toLocaleString()}</TableCell>
                    <TableCell className="text-right">
                      <Button variant="destructive" size="sm" onClick={() => void revoke(key.id)}>
                        <Trash2 className="mr-2 h-3.5 w-3.5" />
                        Revoke
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
                {!loading && keys.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center text-sm text-[var(--muted-foreground)]">
                      No API keys yet.
                    </TableCell>
                  </TableRow>
                ) : null}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
    </section>
  )
}
