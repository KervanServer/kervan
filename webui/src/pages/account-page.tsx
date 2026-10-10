import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { KeyRound, Loader2, Plus, Trash2, UserRound } from "lucide-react"
import { toast } from "sonner"

import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { permissionSummary } from "@/components/shared/policy-fields"
import { StatusMessage } from "@/components/shared/status-message"
import { api } from "@/lib/api"
import { describeEffectiveQuota, describeEffectiveRate } from "@/lib/format"
import { useAuthStore } from "@/stores/auth-store"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Textarea } from "@/components/ui/textarea"

type Props = { token: string }

const PROVIDER_LABELS: Record<string, string> = {
  local: "Kervan password",
  ldap: "LDAP directory",
  oidc: "Single sign-on",
}

export function AccountPage({ token }: Props) {
  const queryClient = useQueryClient()
  const replaceToken = useAuthStore((state) => state.replaceToken)
  const accountQuery = useQuery({ queryKey: ["account", token], queryFn: () => api.account(token) })
  const account = accountQuery.data

  const [passwords, setPasswords] = useState({ current: "", next: "", confirm: "" })
  const [newKeys, setNewKeys] = useState("")

  const changePassword = useMutation({
    mutationFn: () => api.changePassword(token, { current_password: passwords.current, new_password: passwords.next }),
    onSuccess: (result) => {
      // Other sessions are revoked server-side; keep this one alive.
      replaceToken(result.token)
      setPasswords({ current: "", next: "", confirm: "" })
      toast.success("Password changed. Other sessions were signed out.")
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Unable to change password"),
  })

  const saveKeys = useMutation({
    mutationFn: (lines: string[]) => api.setAccountKeys(token, lines),
    onSuccess: async (updated) => {
      queryClient.setQueryData(["account", token], updated)
      setNewKeys("")
      toast.success("SSH keys saved.")
    },
    onError: (error) => toast.error(error instanceof Error ? error.message : "Unable to save SSH keys"),
  })

  const mismatch = passwords.confirm !== "" && passwords.next !== passwords.confirm
  const canChange = passwords.current !== "" && passwords.next !== "" && passwords.next === passwords.confirm
  const existingLines = account?.authorized_keys.map((key) => key.line) ?? []
  const pastedLines = newKeys
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line !== "")

  return (
    <section className="grid gap-4 xl:grid-cols-2">
      <div className="xl:col-span-2">
        <PageHeader title="My Account" description="Your profile, password and the SSH keys you can use for SFTP and SCP." />
      </div>

      {accountQuery.error instanceof Error ? (
        <div className="xl:col-span-2">
          <StatusMessage variant="error">{accountQuery.error.message}</StatusMessage>
        </div>
      ) : null}

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <UserRound className="h-5 w-5" />
            Profile
          </CardTitle>
          <CardDescription>Managed by your administrator.</CardDescription>
        </CardHeader>
        <CardContent>
          {!account ? (
            <Skeleton className="h-32 w-full" />
          ) : (
            <dl className="grid grid-cols-[140px_1fr] gap-y-2 text-sm">
              <dt className="text-[var(--text-secondary)]">Username</dt>
              <dd className="font-medium">{account.username}</dd>
              <dt className="text-[var(--text-secondary)]">Role</dt>
              <dd>{account.type === "admin" ? "Administrator" : "User"}</dd>
              <dt className="text-[var(--text-secondary)]">Sign-in</dt>
              <dd>{PROVIDER_LABELS[account.auth_provider ?? "local"] ?? account.auth_provider}</dd>
              {account.email ? (
                <>
                  <dt className="text-[var(--text-secondary)]">Email</dt>
                  <dd>{account.email}</dd>
                </>
              ) : null}
              <dt className="text-[var(--text-secondary)]">Group</dt>
              <dd>{account.primary_group || "-"}</dd>
              <dt className="text-[var(--text-secondary)]">Home</dt>
              <dd className="font-mono">{account.home_dir || "/"}</dd>
              <dt className="text-[var(--text-secondary)]">Storage quota</dt>
              <dd>{describeEffectiveQuota(account.effective?.max_storage)}</dd>
              <dt className="text-[var(--text-secondary)]">Bandwidth</dt>
              <dd>{describeEffectiveRate(account.effective?.max_bandwidth)}</dd>
              <dt className="text-[var(--text-secondary)]">Permissions</dt>
              <dd>{permissionSummary(account.effective?.permissions)}</dd>
            </dl>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Password</CardTitle>
          <CardDescription>
            {account && !account.password_changeable
              ? "Your password is managed by your identity provider."
              : "Changing it signs out your other sessions."}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {account && account.password_changeable ? (
            <form
              className="space-y-3"
              onSubmit={(event) => {
                event.preventDefault()
                changePassword.mutate()
              }}
            >
              <Input
                aria-label="Current password"
                placeholder="Current password"
                type="password"
                autoComplete="current-password"
                value={passwords.current}
                onChange={(event) => setPasswords({ ...passwords, current: event.target.value })}
              />
              <Input
                aria-label="New password"
                placeholder="New password"
                type="password"
                autoComplete="new-password"
                value={passwords.next}
                onChange={(event) => setPasswords({ ...passwords, next: event.target.value })}
              />
              <Input
                aria-label="Confirm new password"
                placeholder="Confirm new password"
                type="password"
                autoComplete="new-password"
                aria-invalid={mismatch ? "true" : "false"}
                value={passwords.confirm}
                onChange={(event) => setPasswords({ ...passwords, confirm: event.target.value })}
              />
              {mismatch ? <p className="text-sm text-[var(--error)]">The passwords do not match.</p> : null}
              <Button className="w-full" disabled={!canChange || changePassword.isPending}>
                {changePassword.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" /> : null}
                Change password
              </Button>
            </form>
          ) : null}
        </CardContent>
      </Card>

      <Card className="xl:col-span-2">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRound className="h-5 w-5" />
            SSH keys
          </CardTitle>
          <CardDescription>Public keys accepted for SFTP and SCP sign-in.</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          {account && account.authorized_keys.length === 0 ? (
            <EmptyState title="No SSH keys" description="Add a public key (for example ~/.ssh/id_ed25519.pub) to sign in without a password." icon={KeyRound} />
          ) : (
            <ul className="divide-y divide-[var(--border)] rounded-xl border border-[var(--border)]">
              {account?.authorized_keys.map((key) => (
                <li key={key.fingerprint} className="flex items-center justify-between gap-3 p-3 text-sm">
                  <div className="min-w-0">
                    <div className="font-medium">{key.comment || key.type}</div>
                    <div className="truncate font-mono text-xs text-[var(--text-secondary)]">
                      {key.type} {key.fingerprint}
                    </div>
                  </div>
                  <Button
                    size="sm"
                    variant="destructive"
                    aria-label={`Remove key ${key.comment || key.fingerprint}`}
                    disabled={saveKeys.isPending}
                    onClick={() => saveKeys.mutate(existingLines.filter((line) => line !== key.line))}
                  >
                    <Trash2 className="mr-2 h-4 w-4" />
                    Remove
                  </Button>
                </li>
              ))}
            </ul>
          )}
          <Textarea
            aria-label="New SSH public keys"
            placeholder="ssh-ed25519 AAAA... you@laptop (one key per line)"
            className="font-mono text-xs"
            rows={3}
            value={newKeys}
            onChange={(event) => setNewKeys(event.target.value)}
          />
          <Button disabled={pastedLines.length === 0 || saveKeys.isPending} onClick={() => saveKeys.mutate([...existingLines, ...pastedLines])}>
            <Plus className="mr-2 h-4 w-4" />
            Add {pastedLines.length > 1 ? `${pastedLines.length} keys` : "key"}
          </Button>
        </CardContent>
      </Card>
    </section>
  )
}
