import { Suspense, lazy, useEffect, type ReactElement } from "react"
import { ErrorBoundary } from "react-error-boundary"
import { Navigate, Route, Routes, useLocation } from "react-router-dom"

import { ADMIN_ROUTES, AppShell } from "@/components/app-shell"
import { PageSkeleton } from "@/components/shared/page-skeleton"
import { RouteErrorBoundary } from "@/components/shared/route-error-boundary"
import { RouteAnnouncer } from "@/components/shared/route-announcer"
import { LoginForm } from "@/components/login-form"
import { routeModules } from "@/lib/route-modules"
import { matchShareToken } from "@/lib/share-route"
import { describeOIDCError, useAuthStore } from "@/stores/auth-store"

const DashboardPage = lazy(routeModules["/"])
const UsersPage = lazy(routeModules["/users"])
const GroupsPage = lazy(routeModules["/groups"])
const SessionsPage = lazy(routeModules["/sessions"])
const FilesPage = lazy(routeModules["/files"])
const TransfersPage = lazy(routeModules["/transfers"])
const AuditPage = lazy(routeModules["/audit"])
const ConfigurationPage = lazy(routeModules["/configuration"])
const MonitoringPage = lazy(routeModules["/monitoring"])
const ApiKeysPage = lazy(routeModules["/apikeys"])
const AccountPage = lazy(routeModules["/account"])
const SharePage = lazy(() => import("@/pages/share-page").then((m) => ({ default: m.SharePage })))

export function App() {
  const { auth, authError, authLoading, requiresOTP, login, logout, completeOIDC, setAuthError } = useAuthStore()
  const location = useLocation()

  // Finish an SSO round trip: the server redirects back with a one-time
  // code (or an error code), which is removed from the address bar at once.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search)
    const code = params.get("oidc_code")
    const error = params.get("oidc_error")
    if (!code && !error) {
      return
    }
    window.history.replaceState(null, "", window.location.pathname)
    if (code) {
      void completeOIDC(code)
    } else if (error) {
      setAuthError(describeOIDCError(error))
    }
  }, [completeOIDC, setAuthError])

  // Public share links (/s/<token>) need no sign-in.
  const shareToken = matchShareToken(location.pathname)
  if (shareToken) {
    return (
      <Suspense fallback={<div className="min-h-screen bg-[var(--background)]" />}>
        <SharePage token={shareToken} />
      </Suspense>
    )
  }

  if (!auth) {
    return <LoginForm onSubmit={login} loading={authLoading} error={authError} requiresOTP={requiresOTP} />
  }

  // Regular users never see admin pages: those routes, including the
  // dashboard at "/", redirect to their files.
  const isAdmin = auth.user.type === "admin"
  const guard = (path: string, page: ReactElement) => (isAdmin || !ADMIN_ROUTES.has(path) ? page : <Navigate to="/files" replace />)

  return (
    <Suspense fallback={<div className="min-h-screen bg-[var(--background)] p-4 sm:p-6 lg:p-8"><PageSkeleton /></div>}>
      <RouteAnnouncer />
      <AppShell currentUser={auth.user.username} onLogout={logout}>
        <main id="main-content" className="md:pl-16 lg:pl-64">
          <div className="p-4 sm:p-6 lg:p-8">
            <ErrorBoundary FallbackComponent={RouteErrorBoundary}>
              <Suspense fallback={<PageSkeleton />}>
                <Routes>
                  <Route path="/" element={guard("/", <DashboardPage token={auth.token} />)} />
                  <Route path="/users" element={guard("/users", <UsersPage token={auth.token} />)} />
                  <Route path="/groups" element={guard("/groups", <GroupsPage token={auth.token} />)} />
                  <Route path="/sessions" element={guard("/sessions", <SessionsPage token={auth.token} />)} />
                  <Route path="/files" element={guard("/files", <FilesPage token={auth.token} />)} />
                  <Route path="/transfers" element={guard("/transfers", <TransfersPage token={auth.token} />)} />
                  <Route path="/audit" element={guard("/audit", <AuditPage token={auth.token} />)} />
                  <Route path="/configuration" element={guard("/configuration", <ConfigurationPage token={auth.token} />)} />
                  <Route path="/monitoring" element={guard("/monitoring", <MonitoringPage token={auth.token} />)} />
                  <Route path="/apikeys" element={guard("/apikeys", <ApiKeysPage token={auth.token} />)} />
                  <Route path="/account" element={guard("/account", <AccountPage token={auth.token} />)} />
                  <Route path="*" element={<Navigate to={isAdmin ? "/" : "/files"} replace />} />
                </Routes>
              </Suspense>
            </ErrorBoundary>
          </div>
        </main>
      </AppShell>
    </Suspense>
  )
}
