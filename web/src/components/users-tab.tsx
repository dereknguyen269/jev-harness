import { useCallback, useEffect, useState } from "react"
import { Copy, Plus, Trash2 } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { createUser, deleteUser, listUsers, type User } from "@/lib/api"
import { escHtml, isAdminRole } from "@/lib/utils"

interface UserDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onAdded: () => void
}

function UserDialog({ open, onOpenChange, onAdded }: UserDialogProps) {
  const [name, setName] = useState("")
  const [email, setEmail] = useState("")
  const [role, setRole] = useState("viewer")
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [step, setStep] = useState<"form" | "review">("form")
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setName("")
      setEmail("")
      setRole("viewer")
      setErrors({})
      setStep("form")
    }
  }, [open ])

  const review = () => {
    const e: Record<string, string> = {}
    if (!name.trim()) e.name = "Name is required."
    if (email.trim() && !/^\S+@\S+\.\S+$/.test(email.trim())) e.email = "Enter a valid email, or leave blank."
    setErrors(e)
    if (Object.keys(e).length === 0) setStep("review")
  }

  const submit = async () => {
    setSaving(true)
    try {
      await createUser({ name: name.trim(), email: email.trim(), role })
      toast.success("User added")
      onOpenChange(false)
      onAdded()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      if (/name/i.test(msg)) {
        setStep("form")
        setErrors({ name: msg })
      } else if (/email/i.test(msg)) {
        setStep("form")
        setErrors({ email: msg })
      } else {
        toast.error(msg)
      }
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>New user</DialogTitle>
          <DialogDescription>Attribution-only. No authentication is enforced.</DialogDescription>
        </DialogHeader>
        {step === "form" ? (
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="u-name">
                Name <span className="text-destructive">*</span>
              </Label>
              <Input
                id="u-name"
                value={name}
                autoComplete="off"
                aria-required="true"
                aria-invalid={!!errors.name}
                onChange={(e) => {
                  setName(e.target.value)
                  setErrors((p) => ({ ...p, name: "" }))
                }}
              />
              {errors.name && <p className="text-xs text-destructive">{errors.name}</p>}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="u-email">
                Email <span className="font-normal text-muted-foreground">optional</span>
              </Label>
              <Input
                id="u-email"
                type="email"
                value={email}
                autoComplete="off"
                aria-invalid={!!errors.email}
                onChange={(e) => {
                  setEmail(e.target.value)
                  setErrors((p) => ({ ...p, email: "" }))
                }}
              />
              {errors.email && <p className="text-xs text-destructive">{errors.email}</p>}
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="u-role">Role</Label>
              <Select value={role} onValueChange={setRole}>
                <SelectTrigger id="u-role">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="viewer">viewer</SelectItem>
                  <SelectItem value="operator">operator</SelectItem>
                  <SelectItem value="admin">admin</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>
        ) : (
          <div className="space-y-2">
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              Name: <b>{name.trim()}</b>
            </p>
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              Email: {email.trim() || "–"} · role <b>{role}</b>
            </p>
            <p className="text-sm text-muted-foreground">A random API key is minted on creation.</p>
          </div>
        )}
        <DialogFooter>
          {step === "review" ? (
            <>
              <Button variant="ghost" onClick={() => setStep("form")} disabled={saving}>
                Back
              </Button>
              <Button onClick={submit} disabled={saving}>
                Add
              </Button>
            </>
          ) : (
            <>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button onClick={review}>Review &amp; add</Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function UsersTab({ role }: { role: string }) {
  const [users, setUsers] = useState<User[]>([])
  const [dialogOpen, setDialogOpen] = useState(false)
  const [deleting, setDeleting] = useState<User | null>(null)

  const load = useCallback(async () => {
    try {
      setUsers(await listUsers())
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const doDelete = async () => {
    if (!deleting) return
    try {
      await deleteUser(deleting.id)
      toast.success("User deleted")
      setDeleting(null)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  const copyKey = async (key: string) => {
    try {
      await navigator.clipboard.writeText(key)
      toast.success("Key copied")
    } catch {
      toast.error("Clipboard unavailable")
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <span className="flex-1" />
        {isAdminRole(role) && (
          <Button onClick={() => setDialogOpen(true)}>
            <Plus /> New user
          </Button>
        )}
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Email</TableHead>
              <TableHead>Role</TableHead>
              <TableHead>API key</TableHead>
              <TableHead>Active</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {users.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-muted-foreground">
                  No users yet.
                </TableCell>
              </TableRow>
            )}
            {users.map((u) => (
              <TableRow key={u.id}>
                <TableCell>{u.name}</TableCell>
                <TableCell>{u.email}</TableCell>
                <TableCell>{u.role}</TableCell>
                <TableCell>
                  {u.api_key ? (
                    <>
                      <span className="font-mono text-xs">{u.api_key}</span>{" "}
                      <Button variant="ghost" size="sm" onClick={() => copyKey(u.api_key)}>
                        <Copy /> copy
                      </Button>
                    </>
                  ) : (
                    <span className="text-xs text-muted-foreground">hidden</span>
                  )}
                </TableCell>
                <TableCell>{u.active ? "yes" : "no"}</TableCell>
                <TableCell className="text-right">
                  {isAdminRole(role) ? (
                    <Button
                      variant="destructive"
                      size="sm"
                      aria-label={`delete user ${u.name}`}
                      onClick={() => setDeleting(u)}
                    >
                      <Trash2 /> Delete
                    </Button>
                  ) : (
                    <span className="text-xs text-muted-foreground">read-only</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        Users sign in with their API key. Roles gate access: viewers read, operators approve, admins manage.
        Keys are only visible to admins.
      </p>

      <UserDialog open={dialogOpen} onOpenChange={setDialogOpen} onAdded={load} />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete user"
        lines={deleting ? [`Name: <b>${escHtml(deleting.name)}</b>`, `Email: ${escHtml(deleting.email) || "–"}`] : []}
        confirmLabel="Delete"
        danger
        onConfirm={doDelete}
      />
    </div>
  )
}
