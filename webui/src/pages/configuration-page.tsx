import { useEffect, useState } from "react"
import { RefreshCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { api } from "@/lib/api"

type Props = { token: string }

export function ConfigurationPage({ token }: Props) {
  const [baseConfig, setBaseConfig] = useState<Record<string, unknown>>({})
  const [configDraft, setConfigDraft] = useState("{}")
  const [reloadResult, setReloadResult] = useState<Record<string, unknown> | null>(null)
  const [saveResult, setSaveResult] = useState<Record<string, unknown> | null>(null)
  const [validateResult, setValidateResult] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [validating, setValidating] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadConfig = async () => {
    setLoading(true)
    try {
      const response = await api.serverConfig(token)
      setBaseConfig(response.config)
      setConfigDraft(JSON.stringify(response.config, null, 2))
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load configuration")
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void loadConfig()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  const reload = async () => {
    try {
      const response = await api.reloadServer(token)
      setReloadResult(response)
      setSaveResult(null)
      setValidateResult(null)
      setError(null)
      await loadConfig()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to reload configuration")
    }
  }

  const save = async () => {
    setSaving(true)
    try {
      const parsed = JSON.parse(configDraft) as Record<string, unknown>
      const patch = buildPatch(baseConfig, parsed)
      if (Object.keys(patch).length === 0) {
        setSaveResult({ updated: false, message: "No changes detected." })
        setError(null)
        return
      }
      const response = await api.updateServerConfig(token, patch)
      setSaveResult(response)
      setReloadResult(null)
      setValidateResult(null)
      setError(null)
      await loadConfig()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to update configuration")
    } finally {
      setSaving(false)
    }
  }

  const validatePatch = async () => {
    setValidating(true)
    try {
      const parsed = JSON.parse(configDraft) as Record<string, unknown>
      const patch = buildPatch(baseConfig, parsed)
      if (Object.keys(patch).length === 0) {
        setValidateResult({ validated: true, changed_paths: [], message: "No changes detected." })
        setError(null)
        return
      }
      const response = await api.validateServerConfig(token, patch)
      setValidateResult(response)
      setReloadResult(null)
      setSaveResult(null)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to validate configuration patch")
    } finally {
      setValidating(false)
    }
  }

  return (
    <section className="space-y-4">
      <Card>
        <CardHeader>
          <CardTitle>Runtime Configuration</CardTitle>
          <CardDescription>
            Redacted snapshot from <code>/api/v1/server/config</code>.
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-3">
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => void loadConfig()} disabled={loading}>
              Refresh
            </Button>
            <Button variant="outline" onClick={() => void validatePatch()} disabled={validating}>
              {validating ? "Validating..." : "Validate Patch"}
            </Button>
            <Button variant="secondary" onClick={() => void save()} disabled={saving}>
              {saving ? "Saving..." : "Save Config"}
            </Button>
            <Button onClick={() => void reload()}>
              <RefreshCcw className="mr-2 h-4 w-4" />
              Reload Config
            </Button>
          </div>
          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
          <textarea
            className="min-h-[52vh] w-full rounded-xl border border-[var(--border)] bg-[var(--muted)] p-3 font-mono text-xs"
            value={configDraft}
            onChange={(e) => setConfigDraft(e.target.value)}
            spellCheck={false}
          />
        </CardContent>
      </Card>

      {saveResult ? (
        <Card>
          <CardHeader>
            <CardTitle>Save Result</CardTitle>
          </CardHeader>
          <CardContent>
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
