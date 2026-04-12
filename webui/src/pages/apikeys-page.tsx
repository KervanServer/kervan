import { useEffect, useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Copy, KeyRound, Loader2, ShieldCheck, Trash2 } from "lucide-react"
import { useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod"

import { ConfirmDialog } from "@/components/shared/confirm-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useAPIKeys, useCreateAPIKey, useDeleteAPIKey } from "@/hooks/use-api-keys"
import type { ApiKey, ApiKeyPreset, ApiKeyScopeInfo } from "@/lib/types"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip } from "@/components/ui/tooltip"

type Props = { token: string }

const createAPIKeySchema = z.object({
  name: z
    .string()
    .trim()
    .min(1, "Key name is required")
    .max(80, "Key name must be 80 characters or fewer"),
})

type CreateAPIKeyValues = z.infer<typeof createAPIKeySchema>

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
  const apiKeysQuery = useAPIKeys(token)
  const createAPIKeyMutation = useCreateAPIKey(token)
  const deleteAPIKeyMutation = useDeleteAPIKey(token)

  const keys = apiKeysQuery.data?.keys ?? []
  const supportedScopes = apiKeysQuery.data?.supported_scopes ?? []
  const presets = apiKeysQuery.data?.presets ?? []
  const error = apiKeysQuery.error instanceof Error ? apiKeysQuery.error.message : null

  const [selectedPreset, setSelectedPreset] = useState("read-write")
  const [selectedScopes, setSelectedScopes] = useState<string[]>([])
  const [generatedKey, setGeneratedKey] = useState<string | null>(null)
  const [keyToDelete, setKeyToDelete] = useState<ApiKey | null>(null)

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isValid },
  } = useForm<CreateAPIKeyValues>({
    resolver: zodResolver(createAPIKeySchema),
    mode: "onChange",
    defaultValues: {
      name: "",
    },
  })

  useEffect(() => {
    if (supportedScopes.length === 0) {
      return
    }

    if (presets.length > 0 && selectedScopes.length === 0) {
      const preferredPreset = presets.find((preset) => preset.id === selectedPreset) ?? presets.at(-1)
      if (preferredPreset) {
        setSelectedPreset(preferredPreset.id)
        setSelectedScopes(sortScopes(preferredPreset.scopes, supportedScopes))
      }
      return
    }

    setSelectedScopes((current) => {
      const sorted = sortScopes(current, supportedScopes)
      return sameScopes(current, sorted) ? current : sorted
    })
  }, [presets, selectedPreset, selectedScopes.length, supportedScopes])

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

  const create = async (values: CreateAPIKeyValues) => {
    if (selectedScopes.length === 0) {
      return
    }

    const permissions =
      selectedPreset !== "custom" && presets.some((preset) => preset.id === selectedPreset)
        ? selectedPreset
        : selectedScopes.join(",")

    const response = await createAPIKeyMutation.mutateAsync({ name: values.name.trim(), permissions })
    setGeneratedKey(response.key)
    reset({ name: "" })
  }

  const revoke = async () => {
    if (!keyToDelete) {
      return
    }
    await deleteAPIKeyMutation.mutateAsync(keyToDelete.id)
    setKeyToDelete(null)
  }

  const copyGeneratedKey = async () => {
    if (!generatedKey) {
      return
    }
    try {
      await navigator.clipboard.writeText(generatedKey)
      toast.success("API key copied to clipboard.")
    } catch {
      toast.error("Unable to copy API key")
    }
  }

  return (
    <section className="space-y-4">
      <PageHeader
        title="API Keys"
        description="Issue scoped personal keys for automation, integrations, and read-only operational access."
      />

      <Card>
        <CardHeader>
          <CardTitle>API Keys</CardTitle>
          <CardDescription>Create scoped personal API keys with only the access an integration needs.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <form className="space-y-4" onSubmit={handleSubmit(create)}>
            <div className="grid gap-2 md:grid-cols-[1.2fr_0.8fr]">
              <div className="space-y-2">
                <Input placeholder="Key name" aria-invalid={errors.name ? "true" : "false"} {...register("name")} />
                {errors.name ? <p className="text-sm text-[var(--error)]">{errors.name.message}</p> : null}
              </div>
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
              <Button type="submit" disabled={createAPIKeyMutation.isPending || !isValid || selectedScopes.length === 0}>
                {createAPIKeyMutation.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" /> : <KeyRound className="mr-2 h-4 w-4" />}
                {createAPIKeyMutation.isPending ? "Creating..." : "Create key"}
              </Button>
            </div>
          </form>

          {generatedKey ? (
            <div className="rounded-xl border border-amber-500/40 bg-amber-500/10 p-3" role="status" aria-live="polite">
              <p className="text-sm font-medium">New API key (shown once)</p>
              <p className="mt-1 break-all font-mono text-xs">{generatedKey}</p>
              <div className="mt-2">
                <Tooltip content="Copy the generated API key">
                  <Button size="sm" variant="outline" onClick={() => void copyGeneratedKey()} aria-label="Copy generated API key">
                    <Copy className="mr-2 h-3.5 w-3.5" />
                    Copy
                  </Button>
                </Tooltip>
              </div>
            </div>
          ) : null}

          {error ? <StatusMessage variant="error">{error}</StatusMessage> : null}

          {apiKeysQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 3 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : keys.length === 0 ? (
            <EmptyState
              title="No API keys yet"
              description="Create a scoped API key for automation or service integrations."
              icon={KeyRound}
            />
          ) : (
            <div className="rounded-xl border border-[var(--border)]">
              <div className="overflow-x-auto">
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
                          <Tooltip content={`Revoke ${key.name}`}>
                            <Button
                              variant="destructive"
                              size="sm"
                              onClick={() => setKeyToDelete(key)}
                              aria-label={`Revoke API key ${key.name}`}
                            >
                              <Trash2 className="mr-2 h-3.5 w-3.5" />
                              Revoke
                            </Button>
                          </Tooltip>
                        </TableCell>
                      </TableRow>
                    ))}
                  </TableBody>
                </Table>
              </div>
            </div>
          )}
        </CardContent>
      </Card>

      <ConfirmDialog
        open={keyToDelete !== null}
        title="Revoke API key"
        description={keyToDelete ? `Revoke API key "${keyToDelete.name}"? Integrations using it will stop working immediately.` : ""}
        confirmLabel="Revoke key"
        pending={deleteAPIKeyMutation.isPending}
        onConfirm={() => void revoke()}
        onOpenChange={(open) => {
          if (!open) {
            setKeyToDelete(null)
          }
        }}
      />
    </section>
  )
}
