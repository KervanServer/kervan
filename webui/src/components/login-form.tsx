import { useState } from "react"
import { ShieldCheck } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { ThemeToggle } from "@/components/theme-toggle"

type Props = {
  onSubmit: (username: string, password: string, otp?: string) => Promise<void>
  loading: boolean
  error: string | null
  requiresOTP: boolean
}

export function LoginForm({ onSubmit, loading, error, requiresOTP }: Props) {
  const [username, setUsername] = useState("admin")
  const [password, setPassword] = useState("")
  const [otp, setOTP] = useState("")

  const submit = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    await onSubmit(username.trim(), password, otp.trim())
  }

  return (
    <main className="grid min-h-screen place-items-center p-4">
      <Card className="w-full max-w-md fade-up">
        <CardHeader>
          <div className="mb-2 flex items-center justify-between">
            <ShieldCheck className="h-8 w-8 text-[var(--primary)]" />
            <ThemeToggle />
          </div>
          <CardTitle>Kervan Sign In</CardTitle>
          <CardDescription>
            {requiresOTP
              ? "Password accepted. Enter the 6-digit code from your authenticator app."
              : "Use your admin or virtual user credentials to access Web UI."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={submit}>
            <div className="space-y-2">
              <label htmlFor="username" className="text-sm font-medium">
                Username
              </label>
              <Input
                id="username"
                autoComplete="username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                required
              />
            </div>
            <div className="space-y-2">
              <label htmlFor="password" className="text-sm font-medium">
                Password
              </label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                required
              />
            </div>
            {requiresOTP ? (
              <div className="space-y-2">
                <label htmlFor="otp" className="text-sm font-medium">
                  Two-factor code
                </label>
                <Input
                  id="otp"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  value={otp}
                  onChange={(e) => setOTP(e.target.value)}
                  required
                />
              </div>
            ) : null}
            {error ? <p className="text-sm text-[var(--destructive)]">{error}</p> : null}
            <Button className="w-full" type="submit" disabled={loading}>
              {loading ? (requiresOTP ? "Verifying..." : "Signing in...") : requiresOTP ? "Verify code" : "Sign in"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}



