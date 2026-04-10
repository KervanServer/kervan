import { ArrowDownToLine, ArrowUpFromLine, FileWarning, Server } from "lucide-react"

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card"

type Props = {
  activeSessions: number
  activeTransfers: number
  uploadBytes: number
  downloadBytes: number
}

const toMB = (value: number): string => `${(value / (1024 * 1024)).toFixed(2)} MB`

export function StatsGrid({ activeSessions, activeTransfers, uploadBytes, downloadBytes }: Props) {
  const cards = [
    { label: "Active Sessions", value: activeSessions.toString(), icon: Server },
    { label: "Active Transfers", value: activeTransfers.toString(), icon: FileWarning },
    { label: "Uploaded", value: toMB(uploadBytes), icon: ArrowUpFromLine },
    { label: "Downloaded", value: toMB(downloadBytes), icon: ArrowDownToLine },
  ]

  return (
    <section className="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
      {cards.map((item, index) => (
        <Card key={item.label} className="fade-up" style={{ animationDelay: `${index * 70}ms` }}>
          <CardHeader className="pb-2">
            <CardTitle className="text-sm text-[var(--muted-foreground)]">{item.label}</CardTitle>
          </CardHeader>
          <CardContent className="flex items-end justify-between">
            <p className="text-2xl font-bold">{item.value}</p>
            <item.icon className="h-5 w-5 text-[var(--primary)]" />
          </CardContent>
        </Card>
      ))}
    </section>
  )
}


