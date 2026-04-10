import { Activity, ArrowLeftRight, FolderKanban, House, KeyRound, LogOut, Menu, ScrollText, ServerCog, Settings2, UsersRound } from "lucide-react"
import { NavLink } from "react-router-dom"
import { useState } from "react"

import { ThemeToggle } from "@/components/theme-toggle"
import { Button } from "@/components/ui/button"
import { cn } from "@/lib/utils"

type Item = { to: string; icon: typeof House; label: string }

const items: Item[] = [
  { to: "/", icon: House, label: "Dashboard" },
  { to: "/users", icon: UsersRound, label: "Users" },
  { to: "/sessions", icon: ServerCog, label: "Sessions" },
  { to: "/files", icon: FolderKanban, label: "Files" },
  { to: "/transfers", icon: ArrowLeftRight, label: "Transfers" },
  { to: "/audit", icon: ScrollText, label: "Audit" },
  { to: "/monitoring", icon: Activity, label: "Monitoring" },
  { to: "/apikeys", icon: KeyRound, label: "API Keys" },
  { to: "/configuration", icon: Settings2, label: "Configuration" },
]

type Props = {
  currentUser: string
  onLogout: () => void
}

function NavItems({ onClick }: { onClick?: () => void }) {
  return (
    <>
      {items.map((item) => (
        <NavLink
          key={item.to}
          to={item.to}
          onClick={onClick}
          end={item.to === "/"}
          className={({ isActive }) =>
            cn(
              "flex items-center gap-2 rounded-xl px-3 py-2 text-sm transition-colors",
              isActive ? "bg-[var(--primary)] text-[var(--primary-foreground)]" : "text-[var(--foreground)] hover:bg-[var(--muted)]",
            )
          }
        >
          <item.icon className="h-4 w-4" />
          <span>{item.label}</span>
        </NavLink>
      ))}
    </>
  )
}

export function AppShell({ currentUser, onLogout }: Props) {
  const [mobileOpen, setMobileOpen] = useState(false)

  return (
    <div className="mx-auto flex min-h-screen w-full max-w-[1320px] gap-4 p-3 md:p-5">
      <aside className="glass hidden w-64 shrink-0 rounded-2xl p-3 md:block fade-up">
        <div className="mb-5 rounded-xl border border-[var(--border)] bg-[var(--muted)]/70 px-3 py-2 text-sm">
          Signed in as <strong>{currentUser}</strong>
        </div>
        <div className="space-y-1">
          <NavItems />
        </div>
      </aside>

      <div className="flex min-w-0 flex-1 flex-col gap-4 fade-up">
        <header className="glass flex items-center justify-between rounded-2xl p-3">
          <div className="flex items-center gap-2">
            <Button variant="outline" size="icon" className="md:hidden" onClick={() => setMobileOpen((v) => !v)}>
              <Menu className="h-4 w-4" />
            </Button>
            <div>
              <p className="text-xs uppercase tracking-[0.12em] text-[var(--muted-foreground)]">Kervan Server</p>
              <h1 className="text-xl font-semibold">Control Center</h1>
            </div>
          </div>
          <div className="flex items-center gap-2">
            <ThemeToggle />
            <Button variant="outline" size="sm" onClick={onLogout}>
              <LogOut className="mr-2 h-4 w-4" />
              Log out
            </Button>
          </div>
        </header>

        {mobileOpen ? (
          <nav className="glass rounded-2xl p-2 md:hidden">
            <div className="space-y-1">
              <NavItems onClick={() => setMobileOpen(false)} />
            </div>
          </nav>
        ) : null}
      </div>
    </div>
  )
}
