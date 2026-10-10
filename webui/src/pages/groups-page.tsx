import { useState } from "react"
import { Layers, Loader2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react"

import { ConfirmDialog } from "@/components/shared/confirm-dialog"
import { EmptyState } from "@/components/shared/empty-state"
import { PageHeader } from "@/components/shared/page-header"
import { BandwidthField, DEFAULT_PERMISSIONS, PermissionsEditor, QuotaField, permissionSummary } from "@/components/shared/policy-fields"
import { StatusMessage } from "@/components/shared/status-message"
import { useCreateGroup, useDeleteGroup, useGroups, useUpdateGroup } from "@/hooks/use-groups"
import { describeQuota, describeRate } from "@/lib/format"
import type { ApiGroup, ApiPermissions } from "@/lib/types"
import { Button } from "@/components/ui/button"
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card"
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Skeleton } from "@/components/ui/skeleton"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"

type Props = { token: string }

type GroupDraft = {
  name: string
  description: string
  permissions: ApiPermissions
  max_storage: number
  max_bandwidth: number
}

const NAME_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$/

const emptyDraft = (): GroupDraft => ({
  name: "",
  description: "",
  permissions: { ...DEFAULT_PERMISSIONS },
  max_storage: 0,
  max_bandwidth: 0,
})

function nameError(name: string): string | null {
  if (name.trim() === "") return null
  return NAME_PATTERN.test(name.trim())
    ? null
    : "Use 1-64 letters, digits, '.', '_' or '-', starting with a letter or digit."
}

type GroupFormProps = {
  draft: GroupDraft
  onChange: (next: GroupDraft) => void
  idPrefix: string
}

function GroupFormFields({ draft, onChange, idPrefix }: GroupFormProps) {
  const error = nameError(draft.name)
  return (
    <div className="space-y-3">
      <div className="space-y-2">
        <Input
          aria-label="Group name"
          placeholder="Group name"
          value={draft.name}
          aria-invalid={error ? "true" : "false"}
          onChange={(event) => onChange({ ...draft, name: event.target.value })}
        />
        {error ? <p className="text-sm text-[var(--error)]">{error}</p> : null}
      </div>
      <Input
        aria-label="Description"
        placeholder="Description (optional)"
        value={draft.description}
        onChange={(event) => onChange({ ...draft, description: event.target.value })}
      />
      <PermissionsEditor
        idPrefix={idPrefix}
        value={draft.permissions}
        onChange={(permissions) => onChange({ ...draft, permissions })}
      />
      <QuotaField
        idPrefix={idPrefix}
        value={draft.max_storage}
        inheritLabel="Server default"
        onChange={(max_storage) => onChange({ ...draft, max_storage })}
      />
      <BandwidthField
        idPrefix={idPrefix}
        value={draft.max_bandwidth}
        inheritLabel="Server default"
        onChange={(max_bandwidth) => onChange({ ...draft, max_bandwidth })}
      />
    </div>
  )
}

const draftValid = (draft: GroupDraft) => draft.name.trim() !== "" && nameError(draft.name) === null

export function GroupsPage({ token }: Props) {
  const groupsQuery = useGroups(token)
  const createGroup = useCreateGroup(token)
  const updateGroup = useUpdateGroup(token)
  const deleteGroup = useDeleteGroup(token)

  const [draft, setDraft] = useState<GroupDraft>(emptyDraft)
  const [editing, setEditing] = useState<{ id: string; draft: GroupDraft } | null>(null)
  const [toDelete, setToDelete] = useState<ApiGroup | null>(null)

  const groups = groupsQuery.data?.groups ?? []
  const error = groupsQuery.error instanceof Error ? groupsQuery.error.message : null

  const onCreate = async (event: React.FormEvent) => {
    event.preventDefault()
    await createGroup.mutateAsync({ ...draft, name: draft.name.trim(), description: draft.description.trim() })
    setDraft(emptyDraft())
  }

  const onSaveEdit = async () => {
    if (!editing) return
    await updateGroup.mutateAsync({ id: editing.id, ...editing.draft, name: editing.draft.name.trim() })
    setEditing(null)
  }

  const onConfirmDelete = async () => {
    if (!toDelete) return
    await deleteGroup.mutateAsync({ id: toDelete.id, force: toDelete.member_count > 0 })
    setToDelete(null)
  }

  return (
    <section className="grid gap-4 xl:grid-cols-[1fr_350px]">
      <div className="xl:col-span-2">
        <PageHeader
          title="Groups"
          description="Permission and storage-quota templates. Users inherit them from their primary group unless overridden."
          actions={
            <Button variant="outline" onClick={() => void groupsQuery.refetch()} disabled={groupsQuery.isFetching}>
              <RefreshCw className={`mr-2 h-4 w-4 ${groupsQuery.isFetching ? "animate-spin motion-reduce:animate-none" : ""}`} />
              {groupsQuery.isFetching ? "Refreshing..." : "Refresh"}
            </Button>
          }
        />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Groups</CardTitle>
          <CardDescription>{groupsQuery.isLoading ? "Loading..." : `${groups.length} groups`}</CardDescription>
        </CardHeader>
        <CardContent>
          {error ? <StatusMessage variant="error" className="mb-3">{error}</StatusMessage> : null}
          {groupsQuery.isLoading ? (
            <div className="space-y-3">
              {Array.from({ length: 3 }).map((_, index) => (
                <Skeleton key={index} className="h-12 w-full" />
              ))}
            </div>
          ) : groups.length === 0 ? (
            <EmptyState
              title="No groups yet"
              description="Create a group to share permissions and quotas across users, or to map identity-provider groups."
              icon={Layers}
            />
          ) : (
            <div className="overflow-x-auto rounded-xl border border-[var(--border)]">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Name</TableHead>
                    <TableHead>Members</TableHead>
                    <TableHead>Quota</TableHead>
                    <TableHead>Bandwidth</TableHead>
                    <TableHead>Permissions</TableHead>
                    <TableHead className="text-right">Action</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {groups.map((group) => (
                    <TableRow key={group.id}>
                      <TableCell>
                        <div className="font-medium">{group.name}</div>
                        {group.description ? (
                          <div className="text-xs text-[var(--text-secondary)]">{group.description}</div>
                        ) : null}
                      </TableCell>
                      <TableCell>{group.member_count}</TableCell>
                      <TableCell>{describeQuota(group.max_storage, "Server default")}</TableCell>
                      <TableCell>{describeRate(group.max_bandwidth, "Server default")}</TableCell>
                      <TableCell className="max-w-[260px] text-sm">{permissionSummary(group.permissions)}</TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          <Button
                            size="sm"
                            variant="outline"
                            aria-label={`Edit group ${group.name}`}
                            onClick={() =>
                              setEditing({
                                id: group.id,
                                draft: {
                                  name: group.name,
                                  description: group.description ?? "",
                                  permissions: { ...group.permissions },
                                  max_storage: group.max_storage ?? 0,
                                  max_bandwidth: group.max_bandwidth ?? 0,
                                },
                              })
                            }
                          >
                            <Pencil className="mr-2 h-4 w-4" />
                            Edit
                          </Button>
                          <Button
                            size="sm"
                            variant="destructive"
                            aria-label={`Delete group ${group.name}`}
                            onClick={() => setToDelete(group)}
                          >
                            <Trash2 className="mr-2 h-4 w-4" />
                            Delete
                          </Button>
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

      <Card>
        <CardHeader>
          <CardTitle>Create Group</CardTitle>
          <CardDescription>Members get these permissions and this quota by default.</CardDescription>
        </CardHeader>
        <CardContent>
          <form className="space-y-3" onSubmit={(event) => void onCreate(event)}>
            <GroupFormFields idPrefix="create-group" draft={draft} onChange={setDraft} />
            <Button className="w-full" disabled={createGroup.isPending || !draftValid(draft)}>
              {createGroup.isPending ? (
                <Loader2 className="mr-2 h-4 w-4 animate-spin motion-reduce:animate-none" />
              ) : (
                <Plus className="mr-2 h-4 w-4" />
              )}
              {createGroup.isPending ? "Creating..." : "Create group"}
            </Button>
          </form>
        </CardContent>
      </Card>

      <Dialog open={editing !== null} onOpenChange={(open) => (!open ? setEditing(null) : undefined)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit group</DialogTitle>
            <DialogDescription>Renaming a group updates every member's reference to it.</DialogDescription>
          </DialogHeader>
          {editing ? (
            <GroupFormFields
              idPrefix="edit-group"
              draft={editing.draft}
              onChange={(next) => setEditing({ ...editing, draft: next })}
            />
          ) : null}
          <DialogFooter>
            <Button variant="outline" onClick={() => setEditing(null)}>
              Cancel
            </Button>
            <Button
              onClick={() => void onSaveEdit()}
              disabled={updateGroup.isPending || !editing || !draftValid(editing.draft)}
            >
              {updateGroup.isPending ? "Saving..." : "Save"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={toDelete !== null}
        title="Delete group"
        description={
          toDelete
            ? toDelete.member_count > 0
              ? `"${toDelete.name}" has ${toDelete.member_count} member(s). Deleting it removes their membership; they fall back to their own permissions and the default quota.`
              : `Delete group "${toDelete.name}"?`
            : ""
        }
        confirmLabel="Delete group"
        pending={deleteGroup.isPending}
        onConfirm={() => void onConfirmDelete()}
        onOpenChange={(open) => {
          if (!open) setToDelete(null)
        }}
      />
    </section>
  )
}
