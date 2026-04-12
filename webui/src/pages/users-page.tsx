import { useState } from "react"
import { zodResolver } from "@hookform/resolvers/zod"
import { Download, Loader2, RefreshCw, Shield, Trash2, Upload, UserPlus, UsersRound } from "lucide-react"
import { useForm } from "react-hook-form"
import { toast } from "sonner"
import { z } from "zod"

import { ConfirmDialog } from "@/components/shared/confirm-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { StatusMessage } from "@/components/shared/status-message"
import { useCreateUser, useDeleteUser, useImportUsers, useUpdateUser, useUsers } from "@/hooks/use-users"
import { api } from "@/lib/api"
import type { ApiUser, ApiUserImportReport } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Tooltip } from "@/components/ui/tooltip"

type Props = { token: string }

const createUserSchema = z.object({
  username: z
    .string()
    .trim()
    .min(1, "Username is required")
    .max(64, "Username must be 64 characters or fewer"),
  password: z.string().min(12, "Password must be at least 12 characters"),
  home_dir: z.string().trim().min(1, "Home directory is required"),
  admin: z.boolean(),
})

type CreateUserValues = z.infer<typeof createUserSchema>

export function UsersPage({ token }: Props) {
  const usersQuery = useUsers(token)
  const createUserMutation = useCreateUser(token)
  const updateUserMutation = useUpdateUser(token)
  const deleteUserMutation = useDeleteUser(token)
  const [importReport, setImportReport] = useState<ApiUserImportReport | null>(null)
  const importUsersMutation = useImportUsers(token, setImportReport)

  const [importFile, setImportFile] = useState<File | null>(null)
  const [exporting, setExporting] = useState<"json" | "csv" | null>(null)
  const [userToDelete, setUserToDelete] = useState<ApiUser | null>(null)

  const {
    register,
    handleSubmit,
    reset,
    formState: { errors, isValid },
  } = useForm<CreateUserValues>({
    resolver: zodResolver(createUserSchema),
    mode: "onChange",
    defaultValues: {
      username: "",
      password: "",
      home_dir: "/",
      admin: false,
    },
  })

  const users = usersQuery.data?.users ?? []
  const error = usersQuery.error instanceof Error ? usersQuery.error.message : null

  const onCreate = async (values: CreateUserValues) => {
    await createUserMutation.mutateAsync(values)
    reset({
      username: "",
      password: "",
      home_dir: "/",
      admin: false,
    })
  }

  const onConfirmDelete = async () => {
    if (!userToDelete) {
      return
    }
    await deleteUserMutation.mutateAsync(userToDelete.id)
    setUserToDelete(null)
  }

  const onToggleEnabled = async (user: ApiUser) => {
    await updateUserMutation.mutateAsync({ id: user.id, enabled: !user.enabled })
  }

  const onImport = async () => {
    if (!importFile) {
      toast.error("Choose a CSV or JSON file first")
      return
    }
    await importUsersMutation.mutateAsync(importFile)
    setImportFile(null)
  }

  const onExport = async (format: "json" | "csv") => {
    setExporting(format)
    try {
      const { blob, filename } = await api.exportUsers(token, format)
      const url = URL.createObjectURL(blob)
      const link = document.createElement("a")
      link.href = url
      link.download = filename ?? `users.${format}`
      document.body.appendChild(link)
      link.click()
      link.remove()
      URL.revokeObjectURL(url)
      toast.success(`Users exported as ${format.toUpperCase()}.`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : "Unable to export users")
    } finally {
      setExporting(null)
    }
  }

  return (
    <section className="grid gap-4 xl:grid-cols-[1fr_350px]">
      <div className="xl:col-span-2">
        <PageHeader
          title="Users"
          description="Manage virtual accounts, admin access, and bulk provisioning for protocol users."
          actions={
            <Button variant="outline" onClick={() => void usersQuery.refetch()} disabled={usersQuery.isFetching}>
              <RefreshCw className={`mr-2 h-4 w-4 ${usersQuery.isFetching ? "animate-spin motion-reduce:animate-none" : ""}`} />
              {usersQuery.isFetching ? "Refreshing..." : "Refresh"}
            </Button>
          }
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Users</CardTitle>
          <CardDescription>{usersQuery.isLoading ? "Loading..." : `${users.length} users found`}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}

          {usersQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 4 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : users.length === 0 ? (
            <EmptyState
              title="No users found"
              description="Create your first virtual account to start managing protocol access."
              icon={UsersRound}
            />
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)]">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Username</TableHead>
                    <TableHead>Type</TableHead>
                    <TableHead>Status</TableHead>
                    <TableHead>Home</TableHead>
                    <TableHead className="text-right">Action</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {users.map((user) => (
                    <TableRow key={user.id}>
                      <TableCell>{user.username}</TableCell>
                      <TableCell>
                        <span className="inline-flex items-center gap-1">
                          {user.type === "admin" ? <Shield className="h-3.5 w-3.5" /> : null}
                          {user.type}
                        </span>
                      </TableCell>
                      <TableCell>{user.enabled ? "Enabled" : "Disabled"}</TableCell>
                      <TableCell className="max-w-[200px] truncate">{user.home_dir || "/"}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          <Tooltip content={`${user.enabled ? "Disable" : "Enable"} ${user.username}`}>
                            <Button
                              size="sm"
                              variant="outline"
                              onClick={() => void onToggleEnabled(user)}
                              disabled={updateUserMutation.isPending}
                              aria-label={`${user.enabled ? "Disable" : "Enable"} user ${user.username}`}
                            >
                              {user.enabled ? "Disable" : "Enable"}
                            </Button>
                          </Tooltip>
                          <Tooltip content={`Delete ${user.username}`}>
                            <Button
                              size="sm"
                              variant="destructive"
                              onClick={() => setUserToDelete(user)}
                              aria-label={`Delete user ${user.username}`}
                            >
                              <Trash2 className="mr-2 h-4 w-4" />
                              Delete
                            </Button>
                          </Tooltip>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          )}
        </CardContent>
      </Card>

      <div className="grid gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Create User</CardTitle>
            <CardDescription>Quick virtual account provisioning.</CardDescription>
          </CardHeader>
          <CardContent>
            <form className="space-y-3" onSubmit={handleSubmit(onCreate)}>
              <div className="space-y-2">
                <Input placeholder="Username" aria-invalid={errors.username ? "true" : "false"} {...register("username")} />
                {errors.username ? <p className="text-sm text-[var(--error)]">{errors.username.message}</p> : null}
              </div>
              <div className="space-y-2">
                <Input
                  placeholder="Password"
                  type="password"
                  aria-invalid={errors.password ? "true" : "false"}
                  {...register("password")}
                />
                {errors.password ? <p className="text-sm text-[var(--error)]">{errors.password.message}</p> : null}
              </div>
              <div className="space-y-2">
                <Input
                  placeholder="Home directory"
                  aria-invalid={errors.home_dir ? "true" : "false"}
                  {...register("home_dir")}
                />
                {errors.home_dir ? <p className="text-sm text-[var(--error)]">{errors.home_dir.message}</p> : null}
              </div>
              <label className="flex min-h-11 items-center gap-2 text-sm">
                <input type="checkbox" {...register("admin")} />
                Administrator
              </label>
              <Button className="w-full" disabled={createUserMutation.isPending || !isValid}>
                {createUserMutation.isPending ? <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" /> : <UserPlus className="mr-2 h-4 w-4" />}
                {createUserMutation.isPending ? "Creating..." : "Create"}
              </Button>
            </form>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>Bulk Tools</CardTitle>
            <CardDescription>Import CSV/JSON files or export the current user list.</CardDescription>
          </CardHeader>
          <CardContent className="space-y-3">
            <Input
              type="file"
              accept=".csv,.json,text/csv,application/json"
              onChange={(event) => setImportFile(event.target.files?.[0] ?? null)}
            />
            <Button className="w-full" variant="secondary" disabled={importUsersMutation.isPending} onClick={() => void onImport()}>
              <Upload className="mr-2 h-4 w-4" />
              {importUsersMutation.isPending ? "Importing..." : "Import Users"}
            </Button>
            <div className="grid gap-2 sm:grid-cols-2">
              <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("csv")}>
                <Download className="mr-2 h-4 w-4" />
                {exporting === "csv" ? "Exporting..." : "Export CSV"}
              </Button>
              <Button variant="outline" disabled={exporting !== null} onClick={() => void onExport("json")}>
                <Download className="mr-2 h-4 w-4" />
                {exporting === "json" ? "Exporting..." : "Export JSON"}
              </Button>
            </div>
            {importFile ? <p className="text-xs text-[var(--muted-foreground)]">Selected: {importFile.name}</p> : null}
            {importReport ? (
              <div className="rounded-xl border border-[var(--border)] bg-[var(--muted)]/50 p-3 text-sm" role="status" aria-live="polite">
                <p>
                  Imported via {importReport.format}: {importReport.created} created, {importReport.skipped} skipped.
                </p>
                {importReport.errors && importReport.errors.length > 0 ? (
                  <div className="mt-2 space-y-1 text-xs text-[var(--muted-foreground)]">
                    {importReport.errors.slice(0, 5).map((item) => (
                      <p key={`${item.row}-${item.username ?? "row"}`}>
                        Row {item.row}
                        {item.username ? ` (${item.username})` : ""}: {item.error}
                      </p>
                    ))}
                  </div>
                ) : null}
              </div>
            ) : null}
          </CardContent>
        </Card>
      </div>

      <ConfirmDialog
        open={userToDelete !== null}
        title="Delete user"
        description={userToDelete ? `Delete user "${userToDelete.username}"? This action cannot be undone.` : ""}
        confirmLabel="Delete user"
        pending={deleteUserMutation.isPending}
        onConfirm={() => void onConfirmDelete()}
        onOpenChange={(open) => {
          if (!open) {
            setUserToDelete(null)
          }
        }}
      />
    </section>
  )
}
