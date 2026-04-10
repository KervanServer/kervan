import { Navigate, Route, Routes } from "react-router-dom"
import { useState } from "react"

import { AppShell } from "@/components/app-shell"
import { LoginForm } from "@/components/login-form"
import { DashboardPage } from "@/pages/dashboard-page"
import { UsersPage } from "@/pages/users-page"
import { SessionsPage } from "@/pages/sessions-page"
import { FilesPage } from "@/pages/files-page"
import { TransfersPage } from "@/pages/transfers-page"
import { AuditPage } from "@/pages/audit-page"
import { ConfigurationPage } from "@/pages/configuration-page"
import { api } from "@/lib/api"
import type { AuthUser } from "@/lib/types"

type AuthState = {
  token: string
  user: AuthUser
}

export function App() {
  const [auth, setAuth] = useState<AuthState | null>(null)
  const [authError, setAuthError] = useState<string | null>(null)
  const [authLoading, setAuthLoading] = useState(false)

  const login = async (username: string, password: string) => {
    setAuthLoading(true)
    try {
      const result = await api.login(username, password)
      setAuth(result)
      setAuthError(null)
    } catch (err) {
      setAuthError(err instanceof Error ? err.message : "Login failed")
    } finally {
      setAuthLoading(false)
    }
  }

  if (!auth) {
    return <LoginForm onSubmit={login} loading={authLoading} error={authError} />
  }

  return (
    <div className="min-h-screen pb-6">
      <AppShell currentUser={auth.user.username} onLogout={() => setAuth(null)} />
      <main className="mx-auto mt-4 w-full max-w-[1320px] px-3 md:px-5">
        <Routes>
          <Route path="/" element={<DashboardPage token={auth.token} />} />
          <Route path="/users" element={<UsersPage token={auth.token} />} />
          <Route path="/sessions" element={<SessionsPage token={auth.token} />} />
          <Route path="/files" element={<FilesPage token={auth.token} />} />
          <Route path="/transfers" element={<TransfersPage token={auth.token} />} />
          <Route path="/audit" element={<AuditPage token={auth.token} />} />
          <Route path="/configuration" element={<ConfigurationPage token={auth.token} />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </main>
    </div>
  )
}


