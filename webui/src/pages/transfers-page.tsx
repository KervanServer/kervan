import { useEffect, useState } from "react"
import { ArrowDownUp, RefreshCw } from "lucide-react"

import { ConnectionBanner } from "@/components/shared/connection-banner"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useTransfers } from "@/hooks/use-transfers"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { useLiveSnapshot } from "@/lib/use-live-snapshot"
import type { ApiTransfer } from "@/lib/types"

type Props = { token: string }

export function TransfersPage({ token }: Props) {
  const transfersQuery = useTransfers(token)
  const [active, setActive] = useState<ApiTransfer[]>([])
  const [recent, setRecent] = useState<ApiTransfer[]>([])
  const { snapshot, connected, error: liveError } = useLiveSnapshot(token, ["transfers"])

  useEffect(() => {
    if (transfersQuery.data) {
      setActive(transfersQuery.data.active)
      setRecent(transfersQuery.data.recent)
    }
  }, [transfersQuery.data])

  useEffect(() => {
    if (snapshot?.transfers?.active) {
      setActive(snapshot.transfers.active)
    }
    if (snapshot?.transfers?.recent) {
      setRecent(snapshot.transfers.recent)
    }
  }, [snapshot])

  const rows = [...active, ...recent]
  const error = transfersQuery.error instanceof Error ? transfersQuery.error.message : null

  return (
    <section className="space-y-4">
      <PageHeader
        title="Transfers"
        description="Track live uploads and downloads across protocols with a continuously refreshed activity view."
        actions={
          <>
            <Badge variant="outline">{connected ? "Live stream" : "Snapshot mode"}</Badge>
            <Button variant="outline" onClick={() => void transfersQuery.refetch()} disabled={transfersQuery.isFetching}>
              <RefreshCw className={`mr-2 h-4 w-4 ${transfersQuery.isFetching ? "animate-spin motion-reduce:animate-none" : ""}`} />
              {transfersQuery.isFetching ? "Refreshing..." : "Refresh"}
            </Button>
          </>
        }
      />

      <ConnectionBanner message={liveError} onRetry={() => void transfersQuery.refetch()} />

      <Card>
        <CardHeader>
          <CardTitle>Transfer Activity</CardTitle>
          <CardDescription>
            {active.length} active, {recent.length} recent. {connected ? "Live stream on." : "Fallback mode."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}
          {transfersQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 4 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : rows.length === 0 ? (
            <EmptyState
              title="No transfers yet"
              description="Uploads and downloads will appear here once users start moving data."
              icon={ArrowDownUp}
            />
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)]">
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
            </div>
          )}
        </CardContent>
      </Card>
    </section>
  )
}
