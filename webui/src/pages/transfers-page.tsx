import { useEffect, useState } from "react"

import { Badge } from "@/components/ui/badge"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiTransfer } from "@/lib/types"

type Props = { token: string }

export function TransfersPage({ token }: Props) {
  const [active, setActive] = useState<ApiTransfer[]>([])
  const [recent, setRecent] = useState<ApiTransfer[]>([])
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let cancelled = false

    const load = async () => {
      try {
        const data = await api.transfers(token)
        if (!cancelled) {
          setActive(data.active)
          setRecent(data.recent)
          setError(null)
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "Unable to load transfers")
        }
      }
    }

    void load()
    const id = window.setInterval(() => void load(), 5000)
    return () => {
      cancelled = true
      window.clearInterval(id)
    }
  }, [token])

  const rows = [...active, ...recent]

  return (
    <Card>
      <CardHeader>
        <CardTitle>Transfers</CardTitle>
        <CardDescription>
          {active.length} active, {recent.length} recent.
        </CardDescription>
      </CardHeader>
      <CardContent>
        {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>User</TableHead>
              <TableHead>Direction</TableHead>
              <TableHead>Protocol</TableHead>
              <TableHead>Path</TableHead>
              <TableHead>Status</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((item) => (
              <TableRow key={item.id}>
                <TableCell>{item.username}</TableCell>
                <TableCell>{item.direction}</TableCell>
                <TableCell>{item.protocol}</TableCell>
                <TableCell className="max-w-[260px] truncate">{item.path}</TableCell>
                <TableCell>
                  <Badge variant={item.status === "failed" ? "outline" : "secondary"}>{item.status}</Badge>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  )
}


