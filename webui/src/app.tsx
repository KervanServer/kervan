import { Suspense, lazy } from "react"
import { ErrorBoundary } from "react-error-boundary"
import { Navigate, Route, Routes } from "react-router-dom"

import { AppShell } from "@/components/app-shell"
import { PageSkeleton } from "@/components/shared/page-skeleton"
import { RouteErrorBoundary } from "@/components/shared/route-error-boundary"
import { RouteAnnouncer } from "@/components/shared/route-announcer"
import { LoginForm } from "@/components/login-form"
import { routeModules } from "@/lib/route-modules"
import { useAuthStore } from "@/stores/auth-store"

const DashboardPage = lazy(routeModules["/"])
const UsersPage = lazy(routeModules["/users"])
const SessionsPage = lazy(routeModules["/sessions"])
const FilesPage = lazy(routeModules["/files"])
const TransfersPage = lazy(routeModules["/transfers"])
const AuditPage = lazy(routeModules["/audit"])
const ConfigurationPage = lazy(routeModules["/configuration"])
const MonitoringPage = lazy(routeModules["/monitoring"])
const ApiKeysPage = lazy(routeModules["/apikeys"])

export function App() {
  const { auth, authError, authLoading, requiresOTP, login, logout } = useAuthStore()

  if (!auth) {
    return <LoginForm onSubmit={login} loading={authLoading} error={authError} requiresOTP={requiresOTP} />
  }

  return (
    <Suspense fallback={<div className="min-h-screen bg-[var(--background)] p-4 sm:p-6 lg:p-8"><PageSkeleton /></div>}>
      <RouteAnnouncer />
      <AppShell currentUser={auth.user.username} onLogout={logout} />
      <main id="main-content" className="md:pl-16 lg:pl-64">
        <div className="p-4 sm:p-6 lg:p-8">
          <ErrorBoundary FallbackComponent={RouteErrorBoundary}>
            <Suspense fallback={<PageSkeleton />}>
              <Routes>
                <Route path="/" element={<DashboardPage token={auth.token} />} />
                <Route path="/users" element={<UsersPage token={auth.token} />} />
                <Route path="/sessions" element={<SessionsPage token={auth.token} />} />
                <Route path="/files" element={<FilesPage token={auth.token} />} />
                <Route path="/transfers" element={<TransfersPage token={auth.token} />} />
                <Route path="/audit" element={<AuditPage token={auth.token} />} />
                <Route path="/configuration" element={<ConfigurationPage token={auth.token} />} />
                <Route path="/monitoring" element={<MonitoringPage token={auth.token} />} />
                <Route path="/apikeys" element={<ApiKeysPage token={auth.token} />} />
                <Route path="*" element={<Navigate to="/" replace />} />
              </Routes>
            </Suspense>
          </ErrorBoundary>
        </div>
      </main>
    </Suspense>
  )
}
