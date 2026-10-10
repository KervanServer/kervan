import type { ComponentType } from "react"

type TokenPageProps = {
  token: string
}

type LazyPageModule = {
  default: ComponentType<TokenPageProps>
}

export type AppRoutePath =
  | "/"
  | "/users"
  | "/groups"
  | "/sessions"
  | "/files"
  | "/transfers"
  | "/audit"
  | "/configuration"
  | "/monitoring"
  | "/apikeys"
  | "/account"

type RouteLoader = () => Promise<LazyPageModule>

export const routeModules: Record<AppRoutePath, RouteLoader> = {
  "/": async () => ({ default: (await import("@/pages/dashboard-page")).DashboardPage }),
  "/users": async () => ({ default: (await import("@/pages/users-page")).UsersPage }),
  "/groups": async () => ({ default: (await import("@/pages/groups-page")).GroupsPage }),
  "/sessions": async () => ({ default: (await import("@/pages/sessions-page")).SessionsPage }),
  "/files": async () => ({ default: (await import("@/pages/files-page")).FilesPage }),
  "/transfers": async () => ({ default: (await import("@/pages/transfers-page")).TransfersPage }),
  "/audit": async () => ({ default: (await import("@/pages/audit-page")).AuditPage }),
  "/configuration": async () => ({ default: (await import("@/pages/configuration-page")).ConfigurationPage }),
  "/monitoring": async () => ({ default: (await import("@/pages/monitoring-page")).MonitoringPage }),
  "/apikeys": async () => ({ default: (await import("@/pages/apikeys-page")).ApiKeysPage }),
  "/account": async () => ({ default: (await import("@/pages/account-page")).AccountPage }),
}

const prefetchedRoutes = new Map<AppRoutePath, Promise<LazyPageModule>>()

export function prefetchRoute(path: AppRoutePath): Promise<LazyPageModule> {
  const existing = prefetchedRoutes.get(path)
  if (existing) {
    return existing
  }

  const pending = routeModules[path]()
  prefetchedRoutes.set(path, pending)
  return pending
}
