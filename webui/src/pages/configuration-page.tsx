import { useEffect, useState } from "react"
import { RefreshCcw, Settings2 } from "lucide-react"

import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useReloadServerConfig, useSaveServerConfig, useServerConfig, useValidateServerConfig } from "@/hooks/use-server-config"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"

type Props = { token: string }

export function ConfigurationPage({ token }: Props) {
  const serverConfigQuery = useServerConfig(token)
  const saveServerConfigMutation = useSaveServerConfig(token)
  const validateServerConfigMutation = useValidateServerConfig(token)
  const reloadServerConfigMutation = useReloadServerConfig(token)

  const [baseConfig, setBaseConfig] = useState<Record<string, unknown>>({})
  const [configDraft, setConfigDraft] = useState("{}")
  const [reloadResult, setReloadResult] = useState<Record<string, unknown> | null>(null)
  const [saveResult, setSaveResult] = useState<Record<string, unknown> | null>(null)
  const [validateResult, setValidateResult] = useState<Record<string, unknown> | null>(null)
  const [localError, setLocalError] = useState<string | null>(null)

  useEffect(() => {
    if (!serverConfigQuery.data?.config) {
      return
    }
    setBaseConfig(serverConfigQuery.data.config)
    setConfigDraft(JSON.stringify(serverConfigQuery.data.config, null, 2))
  }, [serverConfigQuery.data])

  const refresh = async () => {
    await serverConfigQuery.refetch()
  }

  const reload = async () => {
    const response = await reloadServerConfigMutation.mutateAsync()
    setReloadResult(response)
    setSaveResult(null)
    setValidateResult(null)
    setLocalError(null)
  }

  const save = async () => {
    try {
      const parsed = JSON.parse(configDraft) as Record<string, unknown>
      const patch = buildPatch(baseConfig, parsed)
      if (Object.keys(patch).length === 0) {
        setSaveResult({ updated: false, message: "No changes detected." })
        setLocalError(null)
        return
      }
      const response = await saveServerConfigMutation.mutateAsync(patch)
      setSaveResult(response)
      setReloadResult(null)
      setValidateResult(null)
      setLocalError(null)
    } catch (error) {
      setLocalError(error instanceof Error ? error.message : "Unable to update configuration")
    }
  }

  const validatePatch = async () => {
    try {
      const parsed = JSON.parse(configDraft) as Record<string, unknown>
      const patch = buildPatch(baseConfig, parsed)
      if (Object.keys(patch).length === 0) {
        setValidateResult({ validated: true, changed_paths: [], message: "No changes detected." })
        setLocalError(null)
        return
      }
      const response = await validateServerConfigMutation.mutateAsync(patch)
      setValidateResult(response)
      setReloadResult(null)
      setSaveResult(null)
      setLocalError(null)
    } catch (error) {
      setLocalError(error instanceof Error ? error.message : "Unable to validate configuration patch")
    }
  }

  const queryError = serverConfigQuery.error instanceof Error ? serverConfigQuery.error.message : null
  const error = localError ?? queryError

  return (
    <section className="space-y-4">
      <PageHeader
        title="Configuration"
        description="Inspect the redacted runtime config, validate patch diffs, and reload server settings without leaving the dashboard."
        actions={
          <>
            <Button variant="outline" onClick={() => void refresh()} disabled={serverConfigQuery.isFetching}>
              {serverConfigQuery.isFetching ? "Refreshing..." : "Refresh"}
            </Button>
            <Button variant="outline" onClick={() => void validatePatch()} disabled={validateServerConfigMutation.isPending}>
              {validateServerConfigMutation.isPending ? "Validating..." : "Validate Patch"}
            </Button>
            <Button variant="secondary" onClick={() => void save()} disabled={saveServerConfigMutation.isPending}>
              {saveServerConfigMutation.isPending ? "Saving..." : "Save Config"}
            </Button>
            <Button onClick={() => void reload()} disabled={reloadServerConfigMutation.isPending}>
              <RefreshCcw className="mr-2 h-4 w-4" />
              {reloadServerConfigMutation.isPending ? "Reloading..." : "Reload Config"}
            </Button>
          </>
        }
      />

      <Card>
        <CardHeader>
          <CardTitle>Runtime Configuration</CardTitle>
          <CardDescription>
            Redacted snapshot from <code>/api/v1/server/config</code>.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          {error ? <StatusMessage variant="error">{error}</StatusMessage> : null}

          {serverConfigQuery.isLoading ? (
            <div className="space-y-3">
              <Skeleton className="h-12 w-full" />
              <Skeleton className="min-h-[52vh] w-full" />
            </div>
          ) : serverConfigQuery.data?.config ? (
            <Textarea
              className="min-h-[52vh]"
              value={configDraft}
              onChange={(event) => setConfigDraft(event.target.value)}
              spellCheck={false}
            />
          ) : (
            <EmptyState
              title="No configuration snapshot"
              description="The server did not return a runtime configuration document."
              icon={Settings2}
            />
          )}
        </CardContent>
      </Card>

      {saveResult ? (
        <Card>
          <CardHeader>
            <CardTitle>Save Result</CardTitle>
          </CardHeader>
          <CardContent>
            <StatusMessage variant="success" className="mb-3">
              Configuration save response received.
            </StatusMessage>
            <pre className="max-h-[30vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
              {JSON.stringify(saveResult, null, 2)}
            </pre>
          </CardContent>
        </Card>
      ) : null}

      {validateResult ? (
        <Card>
          <CardHeader>
            <CardTitle>Validation Result</CardTitle>
          </CardHeader>
          <CardContent>
            <StatusMessage variant="info" className="mb-3">
              Configuration patch validation completed.
            </StatusMessage>
            <pre className="max-h-[30vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
              {JSON.stringify(validateResult, null, 2)}
            </pre>
          </CardContent>
        </Card>
      ) : null}

      {reloadResult ? (
        <Card>
          <CardHeader>
            <CardTitle>Reload Result</CardTitle>
          </CardHeader>
          <CardContent>
            <StatusMessage variant="success" className="mb-3">
              Runtime configuration reload completed.
            </StatusMessage>
            <pre className="max-h-[30vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
              {JSON.stringify(reloadResult, null, 2)}
            </pre>
          </CardContent>
        </Card>
      ) : null}
    </section>
  )
}

function buildPatch(base: Record<string, unknown>, edited: Record<string, unknown>): Record<string, unknown> {
  const patch: Record<string, unknown> = {}
  for (const key of Object.keys(edited)) {
    const before = base[key]
    const after = edited[key]

    if (isObject(before) && isObject(after)) {
      const nested = buildPatch(before, after)
      if (Object.keys(nested).length > 0) {
        patch[key] = nested
      }
      continue
    }

    if (!isEqualJSON(before, after)) {
      patch[key] = after
    }
  }
  return patch
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value)
}

function isEqualJSON(a: unknown, b: unknown): boolean {
  try {
    return JSON.stringify(a) === JSON.stringify(b)
  } catch {
    return false
  }
}
