import { useEffect, useState } from "react"
import { RefreshCcw } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { api } from "@/lib/api"

type Props = { token: string }

export function ConfigurationPage({ token }: Props) {
  const [config, setConfig] = useState<Record<string, unknown> | null>(null)
  const [reloadResult, setReloadResult] = useState<Record<string, unknown> | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const loadConfig = async () => {
    setLoading(true)
    try {
      const response = await api.serverConfig(token)
      setConfig(response.config)
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
      setError(null)
      await loadConfig()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to reload configuration")
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
            <Button onClick={() => void reload()}>
              <RefreshCcw className="mr-2 h-4 w-4" />
              Reload Config
            </Button>
          </div>
          {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
          <pre className="max-h-[52vh] overflow-auto rounded-xl bg-[var(--muted)] p-3 text-xs">
            {JSON.stringify(config ?? {}, null, 2)}
          </pre>
        </CardContent>
      </Card>

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

