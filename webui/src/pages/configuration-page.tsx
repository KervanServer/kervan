import { useEffect, useState } from "react"
import { RefreshCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { api } from "@/lib/api"

type Props = { token: string }

export function ConfigurationPage({ token }: Props) {
  const [configDraft, setConfigDraft] = useState("{}")
  const [reloadResult, setReloadResult] = useState<Record<string, unknown> | null>(null)
  const [saveResult, setSaveResult] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadConfig = async () => {
    setLoading(true)
    try {
      const response = await api.serverConfig(token)
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
      const response = await api.updateServerConfig(token, parsed)
      setSaveResult(response)
      setReloadResult(null)
      setError(null)
      await loadConfig()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to update configuration")
    } finally {
      setSaving(false)
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
