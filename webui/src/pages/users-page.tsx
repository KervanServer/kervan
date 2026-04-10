import { useEffect, useState } from "react"
import { Shield, Trash2, UserPlus } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Input } from "@/components/ui/input"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { api } from "@/lib/api"
import type { ApiUser } from "@/lib/types"

type Props = { token: string }

export function UsersPage({ token }: Props) {
  const [users, setUsers] = useState<ApiUser[]>([])
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [creating, setCreating] = useState(false)
  const [newUser, setNewUser] = useState({ username: "", password: "", home_dir: "/", admin: false })

  const load = async () => {
    setLoading(true)
    try {
      const data = await api.users(token)
      setUsers(data.users)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to load users")
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  const onCreate = async (event: React.FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setCreating(true)
    try {
      await api.createUser(token, newUser)
      setNewUser({ username: "", password: "", home_dir: "/", admin: false })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to create user")
    } finally {
      setCreating(false)
    }
  }

  const onDelete = async (id: string) => {
    if (!window.confirm("Delete this user?")) {
      return
    }
    try {
      await api.deleteUser(token, id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to delete user")
    }
  }

  const onToggleEnabled = async (user: ApiUser) => {
    try {
      await api.updateUser(token, { id: user.id, enabled: !user.enabled })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : "Unable to update user")
    }
  }

  return (
    <section className="grid gap-4 xl:grid-cols-[1fr_350px]">
      <Card>
        <CardHeader>
          <CardTitle>Users</CardTitle>
          <CardDescription>{loading ? "Loading..." : `${users.length} users found`}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <p className="mb-3 text-sm text-[var(--destructive)]">{error}</p> : null}
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
                      <Button size="sm" variant="outline" onClick={() => void onToggleEnabled(user)}>
                        {user.enabled ? "Disable" : "Enable"}
                      </Button>
                      <Button size="sm" variant="destructive" onClick={() => void onDelete(user.id)}>
                        <Trash2 className="mr-2 h-4 w-4" />
                        Delete
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Create User</CardTitle>
          <CardDescription>Quick virtual account provisioning.</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-3" onSubmit={onCreate}>
            <Input
              placeholder="Username"
              value={newUser.username}
              onChange={(e) => setNewUser((prev) => ({ ...prev, username: e.target.value }))}
              required
            />
            <Input
              placeholder="Password"
              type="password"
              value={newUser.password}
              onChange={(e) => setNewUser((prev) => ({ ...prev, password: e.target.value }))}
              required
            />
            <Input
              placeholder="Home directory"
              value={newUser.home_dir}
              onChange={(e) => setNewUser((prev) => ({ ...prev, home_dir: e.target.value }))}
            />
            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={newUser.admin}
                onChange={(e) => setNewUser((prev) => ({ ...prev, admin: e.target.checked }))}
              />
              Administrator
            </label>
            <Button className="w-full" disabled={creating}>
              <UserPlus className="mr-2 h-4 w-4" />
              {creating ? "Creating..." : "Create"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </section>
  )
}



