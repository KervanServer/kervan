import { useState } from "react"
import {
  Activity,
  ArrowLeftRight,
  FolderKanban,
  House,
  KeyRound,
  LogOut,
  Menu,
  ScrollText,
  ServerCog,
  Settings2,
  UsersRound,
  X,
  type LucideIcon,
} from "lucide-react"
import { NavLink } from "react-router-dom"

import { ThemeToggle } from "@/components/theme-toggle"
import { prefetchRoute, type AppRoutePath } from "@/lib/route-modules"
import { Button } from "@/components/ui/button"
import { Tooltip } from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

type NavItem = { to: AppRoutePath; icon: LucideIcon; label: string }

const items: NavItem[] = [
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

function NavItems({ onClick, compact = false }: { onClick?: () => void; compact?: boolean }) {
  return (
    <>
      {items.map((item) => {
        const link = (
          <NavLink
            key={item.to}
            to={item.to}
            onClick={onClick}
            onMouseEnter={() => {
              void prefetchRoute(item.to)
            }}
            onFocus={() => {
              void prefetchRoute(item.to)
            }}
            end={item.to === "/"}
            className={({ isActive }) =>
              cn(
                "flex min-h-11 items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition-colors duration-150",
                compact && "justify-center px-0 lg:justify-start lg:px-3",
                isActive
                  ? "bg-[var(--accent)] text-[var(--accent-foreground)]"
                  : "text-[var(--text-primary)] hover:bg-[var(--background-muted)]",
              )
            }
          >
            <item.icon className="h-4 w-4 shrink-0" />
            <span className={compact ? "sr-only lg:not-sr-only lg:inline" : undefined}>{item.label}</span>
          </NavLink>
        )

        return compact ? (
          <Tooltip key={item.to} content={item.label}>
            {link}
          </Tooltip>
        ) : (
          link
        )
      })}
    </>
  )
}

export function AppShell({ currentUser, onLogout }: Props) {
  const [mobileOpen, setMobileOpen] = useState(false)

  return (
    <div className="min-h-screen bg-[var(--background)] text-[var(--text-primary)] transition-colors duration-150">
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-[500] focus:rounded-md focus:bg-[var(--surface)] focus:px-3 focus:py-2"
      >
        Skip to main content
      </a>

      <aside className="fixed inset-y-0 left-0 z-40 hidden w-16 border-r border-[var(--border)] bg-[var(--background-subtle)] px-2 py-5 md:block lg:w-64 lg:px-4">
        <div className="mb-5 flex justify-center lg:hidden">
          <Tooltip content={`Signed in as ${currentUser}`}>
            <div
              className="flex h-10 w-10 items-center justify-center rounded-full border border-[var(--border)] bg-[var(--surface)] text-sm font-semibold uppercase"
              aria-label={`Signed in as ${currentUser}`}
            >
              {currentUser.slice(0, 1)}
            </div>
          </Tooltip>
        </div>
        <div className="mb-5 hidden rounded-xl border border-[var(--border)] bg-[var(--surface)] px-3 py-3 text-sm lg:block">
          Signed in as <strong>{currentUser}</strong>
        </div>
        <nav className="space-y-1" aria-label="Primary navigation">
          <NavItems compact />
        </nav>
      </aside>

      <header className="sticky top-0 z-[100] flex h-16 items-center justify-between border-b border-[var(--border)] bg-[color-mix(in_srgb,var(--background)_88%,transparent)] px-4 backdrop-blur-sm transition-colors duration-150 sm:px-6 md:pl-24 lg:px-8 lg:pl-[18rem]">
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="icon"
            className="md:hidden"
            onClick={() => setMobileOpen((value) => !value)}
            aria-label="Toggle navigation"
            aria-expanded={mobileOpen}
            aria-controls="mobile-navigation"
          >
            {mobileOpen ? <X className="h-4 w-4" /> : <Menu className="h-4 w-4" />}
          </Button>
          <div>
            <p className="text-xs uppercase tracking-[0.12em] text-[var(--text-secondary)]">Kervan Server</p>
            <h1 className="text-xl font-semibold tracking-tight">Control Center</h1>
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
        <div
          className="fixed inset-0 z-[200] bg-black/30 md:hidden"
          onClick={() => setMobileOpen(false)}
          aria-hidden="true"
        >
          <nav
            id="mobile-navigation"
            className="fixed inset-y-0 left-0 w-72 border-r border-[var(--border)] bg-[var(--background-subtle)] p-4 shadow-lg"
            aria-label="Mobile navigation"
            onClick={(event) => event.stopPropagation()}
          >
            <div className="mb-4 flex items-center justify-between">
              <p className="text-sm font-semibold tracking-tight">Navigation</p>
              <Button variant="outline" size="icon" onClick={() => setMobileOpen(false)} aria-label="Close navigation">
                <X className="h-4 w-4" />
              </Button>
            </div>
            <div className="space-y-1">
              <NavItems onClick={() => setMobileOpen(false)} />
            </div>
          </nav>
        </div>
      ) : null}
    </div>
  )
}
