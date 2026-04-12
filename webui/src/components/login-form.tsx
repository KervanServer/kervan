import { zodResolver } from "@hookform/resolvers/zod"
import { Loader2, ShieldCheck } from "lucide-react"
import { useForm } from "react-hook-form"
import { z } from "zod"

import { ThemeToggle } from "@/components/theme-toggle"
import { StatusMessage } from "@/components/shared/status-message"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"

type Props = {
  onSubmit: (username: string, password: string, otp?: string) => Promise<void>
  loading: boolean
  error: string | null
  requiresOTP: boolean
}

const loginSchema = z.object({
  username: z.string().trim().min(1, "Username is required"),
  password: z.string().min(1, "Password is required"),
  otp: z.string().trim().optional(),
})

type LoginValues = z.infer<typeof loginSchema>

export function LoginForm({ onSubmit, loading, error, requiresOTP }: Props) {
  const {
    register,
    handleSubmit,
    formState: { errors, isValid },
  } = useForm<LoginValues>({
    resolver: zodResolver(loginSchema),
    mode: "onChange",
    defaultValues: {
      username: "admin",
      password: "",
      otp: "",
    },
  })

  const submit = async (values: LoginValues) => {
    await onSubmit(values.username.trim(), values.password, values.otp?.trim())
  }

  return (
    <main className="grid min-h-screen place-items-center bg-[var(--background)] px-4 py-8 text-[var(--text-primary)] transition-colors duration-150">
      <Card className="w-full max-w-md border border-[var(--border)] bg-[var(--surface)] shadow-lg">
        <CardHeader>
          <div className="mb-2 flex items-center justify-between">
            <ShieldCheck className="h-8 w-8 text-[var(--accent)]" />
            <ThemeToggle />
          </div>
          <CardTitle>Kervan Sign In</CardTitle>
          <CardDescription>
            {requiresOTP
              ? "Password accepted. Enter the 6-digit code from your authenticator app."
              : "Use your admin or virtual user credentials to access the control center."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-4" onSubmit={handleSubmit(submit)}>
            <div className="space-y-2">
              <label htmlFor="username" className="text-sm font-medium">
                Username
              </label>
              <Input id="username" autoComplete="username" {...register("username")} aria-invalid={errors.username ? "true" : "false"} />
              {errors.username ? <p className="text-sm text-[var(--error)]">{errors.username.message}</p> : null}
            </div>

            <div className="space-y-2">
              <label htmlFor="password" className="text-sm font-medium">
                Password
              </label>
              <Input
                id="password"
                type="password"
                autoComplete="current-password"
                {...register("password")}
                aria-invalid={errors.password ? "true" : "false"}
              />
              {errors.password ? <p className="text-sm text-[var(--error)]">{errors.password.message}</p> : null}
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
                  {...register("otp")}
                  aria-invalid={errors.otp ? "true" : "false"}
                />
                {errors.otp ? <p className="text-sm text-[var(--error)]">{errors.otp.message}</p> : null}
              </div>
            ) : null}

            {error ? <StatusMessage variant="error">{error}</StatusMessage> : null}

            <Button className="w-full" type="submit" disabled={loading || !isValid}>
              {loading ? <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" /> : null}
              {loading ? (requiresOTP ? "Verifying..." : "Signing in...") : requiresOTP ? "Verify code" : "Sign in"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </main>
  )
}
