import { useEffect, useState, type FormEvent } from "react"
import { Copy, KeyRound, ShieldCheck, Trash2 } from "lucide-react"

import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiKey, ApiKeyPreset, ApiKeyScopeInfo } from "@/lib/types"

type Props = { token: string }

function sortScopes(scopes: string[], supportedScopes: ApiKeyScopeInfo[]): string[] {
  const order = new Map(supportedScopes.map((scope, index) => [scope.name, index]))
  return [...scopes].sort((left, right) => (order.get(left) ?? 999) - (order.get(right) ?? 999))
}

function sameScopes(left: string[], right: string[]): boolean {
  if (left.length !== right.length) {
    return false
  }
  return left.every((scope, index) => scope === right[index])
}

function formatLastUsed(value?: string): string {
  if (!value) {
    return "Never"
  }
  const parsed = new Date(value)
  if (Number.isNaN(parsed.getTime())) {
    return value
  }
  return parsed.toLocaleString()
}

export function ApiKeysPage({ token }: Props) {
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [supportedScopes, setSupportedScopes] = useState<ApiKeyScopeInfo[]>([])
  const [presets, setPresets] = useState<ApiKeyPreset[]>([])
  const [name, setName] = useState("")
  const [selectedPreset, setSelectedPreset] = useState("read-write")
  const [selectedScopes, setSelectedScopes] = useState<string[]>([])
  const [generatedKey, setGeneratedKey] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)

  const load = async () => {
    setLoading(true)
    try {
      const response = await api.apiKeys(token)
      setKeys(response.keys)
      setSupportedScopes(response.supported_scopes)
      setPresets(response.presets)
      if (response.presets.length > 0 && selectedScopes.length === 0) {
        const preferredPreset =
          response.presets.find((preset) => preset.id === selectedPreset) ?? response.presets[response.presets.length - 1]
        setSelectedPreset(preferredPreset.id)
        setSelectedScopes(sortScopes(preferredPreset.scopes, response.supported_scopes))
      } else {
        setSelectedScopes((current) => sortScopes(current, response.supported_scopes))
      }
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

  const applyPreset = (preset: ApiKeyPreset) => {
    setSelectedPreset(preset.id)
    setSelectedScopes(sortScopes(preset.scopes, supportedScopes))
  }

  const toggleScope = (scopeName: string) => {
    setSelectedScopes((current) => {
      const exists = current.includes(scopeName)
      const next = exists ? current.filter((item) => item !== scopeName) : [...current, scopeName]
      const sorted = sortScopes(next, supportedScopes)
      const matchedPreset = presets.find((preset) => sameScopes(sortScopes(preset.scopes, supportedScopes), sorted))
      setSelectedPreset(matchedPreset?.id ?? "custom")
      return sorted
    })
  }

  const create = async (event: FormEvent) => {
    event.preventDefault()
    if (!name.trim() || selectedScopes.length === 0) {
      return
    }
    setCreating(true)
    try {
      const permissions =
        selectedPreset !== "custom" && presets.some((preset) => preset.id === selectedPreset)
          ? selectedPreset
          : selectedScopes.join(",")
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
          <CardDescription>Create scoped personal API keys with only the access an integration needs.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <form className="space-y-4" onSubmit={create}>
            <div className="grid gap-2 md:grid-cols-[1.2fr_0.8fr]">
              <Input placeholder="Key name" value={name} onChange={(event) => setName(event.target.value)} />
              <div className="rounded-xl border border-[var(--border)] bg-[var(--muted)]/40 px-3 py-2 text-sm text-[var(--muted-foreground)]">
                Selected scopes: <span className="font-medium text-[var(--foreground)]">{selectedScopes.length}</span>
              </div>
            </div>

            <div className="space-y-2">
              <p className="text-sm font-medium">Presets</p>
              <div className="grid gap-2 lg:grid-cols-4">
                {presets.map((preset) => (
                  <button
                    key={preset.id}
                    type="button"
                    onClick={() => applyPreset(preset)}
                    className={`rounded-2xl border p-3 text-left transition ${
                      selectedPreset === preset.id
                        ? "border-[var(--primary)] bg-[color-mix(in_oklab,var(--primary)_12%,transparent)]"
                        : "border-[var(--border)] bg-[var(--muted)]/25 hover:bg-[var(--muted)]/50"
                    }`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <p className="font-medium">{preset.label}</p>
                      <Badge variant="outline">{preset.scopes.length} scopes</Badge>
                    </div>
                    <p className="mt-2 text-sm text-[var(--muted-foreground)]">{preset.description}</p>
                  </button>
                ))}
              </div>
            </div>

            <div className="space-y-2">
              <div className="flex items-center justify-between gap-2">
                <p className="text-sm font-medium">Scopes</p>
                {selectedPreset === "custom" ? (
                  <Badge variant="outline" className="gap-1">
                    <ShieldCheck className="h-3.5 w-3.5" />
                    Custom
                  </Badge>
                ) : null}
              </div>
              <div className="grid gap-2 md:grid-cols-2 xl:grid-cols-3">
                {supportedScopes.map((scope) => {
                  const checked = selectedScopes.includes(scope.name)
                  return (
                    <label
                      key={scope.name}
                      className={`flex cursor-pointer gap-3 rounded-2xl border p-3 transition ${
                        checked
                          ? "border-[var(--primary)] bg-[color-mix(in_oklab,var(--primary)_10%,transparent)]"
                          : "border-[var(--border)] bg-[var(--card)]"
                      }`}
                    >
                      <input
                        type="checkbox"
                        className="mt-1 h-4 w-4 accent-[var(--primary)]"
                        checked={checked}
                        onChange={() => toggleScope(scope.name)}
                      />
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-mono text-xs font-semibold">{scope.name}</span>
                          <Badge variant="outline">{scope.access}</Badge>
                        </div>
                        <p className="mt-1 text-sm text-[var(--muted-foreground)]">{scope.description}</p>
                      </div>
                    </label>
                  )
                })}
              </div>
            </div>

            <div className="flex justify-end">
              <Button type="submit" disabled={creating || !name.trim() || selectedScopes.length === 0}>
                <KeyRound className="mr-2 h-4 w-4" />
                {creating ? "Creating..." : "Create key"}
              </Button>
            </div>
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
                  <TableHead>Permissions</TableHead>
                  <TableHead>Prefix</TableHead>
                  <TableHead>Created</TableHead>
                  <TableHead>Last used</TableHead>
                  <TableHead className="text-right">Action</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {keys.map((key) => (
                  <TableRow key={key.id}>
                    <TableCell>{key.name}</TableCell>
                    <TableCell className="max-w-[28rem]">
                      <div className="flex flex-wrap gap-1">
                        {key.permissions.split(",").map((permission) => (
                          <Badge key={permission} variant="outline" className="font-mono text-[11px]">
                            {permission}
                          </Badge>
                        ))}
                      </div>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{key.prefix}</TableCell>
                    <TableCell>{new Date(key.created_at).toLocaleString()}</TableCell>
                    <TableCell>{formatLastUsed(key.last_used)}</TableCell>
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
                    <TableCell colSpan={6} className="text-center text-sm text-[var(--muted-foreground)]">
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
