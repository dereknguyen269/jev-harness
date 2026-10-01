import { useCallback, useEffect, useState } from "react"
import { Pencil, Plus, Trash2 } from "lucide-react"
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
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { ConfirmDialog } from "@/components/confirm-dialog"
import {
  deleteCategory,
  listCategories,
  listRules,
  saveCategory,
  type Category,
} from "@/lib/api"
import { escHtml, isAdminRole } from "@/lib/utils"

interface CategoryDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial: Category
  isEdit: boolean
  onSaved: () => void
}

function CategoryDialog({ open, onOpenChange, initial, isEdit, onSaved }: CategoryDialogProps) {
  const [name, setName] = useState(initial.name)
  const [description, setDescription] = useState(initial.description)
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [step, setStep] = useState<"form" | "review">("form")
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    if (open) {
      setName(initial.name)
      setDescription(initial.description)
      setErrors({})
      setStep("form")
    }
  }, [open, initial])

  const review = () => {
    const e: Record<string, string> = {}
    if (!name.trim()) e.name = "Name is required."
    setErrors(e)
    if (Object.keys(e).length === 0) setStep("review")
  }

  const submit = async () => {
    setSaving(true)
    try {
      const saved = await saveCategory({ name: name.trim(), description: description.trim() }, isEdit)
      toast.success(`Saved category ${saved.name}`)
      onOpenChange(false)
      onSaved()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      setStep("form")
      if (/name/i.test(msg)) setErrors({ name: msg })
      else toast.error(msg)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit category" : "New category"}</DialogTitle>
          <DialogDescription>
            {step === "review"
              ? "Review before saving."
              : "Categories scope rules to a context. Rules referencing a deleted category keep working."}
          </DialogDescription>
        </DialogHeader>
        {step === "form" ? (
          <div className="grid gap-4">
            <div className="space-y-1.5">
              <Label htmlFor="c-name">
                Name <span className="text-destructive">*</span>
              </Label>
              <Input
                id="c-name"
                value={name}
                readOnly={isEdit}
                placeholder="safety"
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
              <Label htmlFor="c-description">Description</Label>
              <Textarea
                id="c-description"
                value={description}
                rows={2}
                placeholder="What this scope covers"
                autoComplete="off"
                onChange={(e) => setDescription(e.target.value)}
              />
            </div>
          </div>
        ) : (
          <div className="space-y-2">
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              Name: <code className="font-mono text-xs">{name.trim()}</code>
            </p>
            {description.trim() && (
              <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">{description.trim()}</p>
            )}
          </div>
        )}
        <DialogFooter>
          {step === "review" ? (
            <>
              <Button variant="ghost" onClick={() => setStep("form")} disabled={saving}>
                Back
              </Button>
              <Button onClick={submit} disabled={saving}>
                {isEdit ? "Save" : "Add"}
              </Button>
            </>
          ) : (
            <>
              <Button variant="ghost" onClick={() => onOpenChange(false)}>
                Cancel
              </Button>
              <Button onClick={review}>{isEdit ? "Review & save" : "Review & add"}</Button>
            </>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function CategoriesTab({ role }: { role: string }) {
  const [categories, setCategories] = useState<Category[]>([])
  const [ruleCounts, setRuleCounts] = useState<Record<string, number>>({})
  const [dialog, setDialog] = useState<{ open: boolean; initial: Category; isEdit: boolean }>({
    open: false,
    initial: { name: "", description: "" },
    isEdit: false,
  })
  const [deleting, setDeleting] = useState<Category | null>(null)

  const load = useCallback(async () => {
    try {
      const [c, r] = await Promise.all([listCategories(), listRules()])
      setCategories(c)
      const counts: Record<string, number> = {}
      for (const rule of r) {
        if (rule.category) counts[rule.category] = (counts[rule.category] ?? 0) + 1
      }
      setRuleCounts(counts)
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
      await deleteCategory(deleting.name)
      toast.success("Category deleted")
      setDeleting(null)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-3">
        <span className="flex-1" />
        {isAdminRole(role) && (
          <Button onClick={() => setDialog({ open: true, initial: { name: "", description: "" }, isEdit: false })}>
            <Plus /> New category
          </Button>
        )}
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Name</TableHead>
              <TableHead>Description</TableHead>
              <TableHead>Rules</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {categories.length === 0 && (
              <TableRow>
                <TableCell colSpan={4} className="py-8 text-center text-muted-foreground">
                  No categories defined yet.
                </TableCell>
              </TableRow>
            )}
            {categories.map((c) => (
              <TableRow key={c.name}>
                <TableCell className="font-mono text-xs">{c.name}</TableCell>
                <TableCell>{c.description || <span className="text-muted-foreground">–</span>}</TableCell>
                <TableCell className="tabular-nums">{ruleCounts[c.name] ?? 0}</TableCell>
                <TableCell className="whitespace-nowrap text-right">
                  {isAdminRole(role) ? (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        aria-label={`edit category ${c.name}`}
                        onClick={() => setDialog({ open: true, initial: c, isEdit: true })}
                      >
                        <Pencil /> Edit
                      </Button>{" "}
                      <Button
                        variant="destructive"
                        size="sm"
                        aria-label={`delete category ${c.name}`}
                        onClick={() => setDeleting(c)}
                      >
                        <Trash2 /> Delete
                      </Button>
                    </>
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
        Deleting a category never touches rules — their category simply becomes undeclared.
      </p>

      <CategoryDialog
        open={dialog.open}
        onOpenChange={(o) => setDialog((d) => ({ ...d, open: o }))}
        initial={dialog.initial}
        isEdit={dialog.isEdit}
        onSaved={load}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete category"
        lines={
          deleting
            ? [
                `Name: <code class="font-mono text-xs">${escHtml(deleting.name)}</code>`,
                `${ruleCounts[deleting.name] ?? 0} rule(s) reference it — they keep working.`,
              ]
            : []
        }
        confirmLabel="Delete"
        danger
        onConfirm={doDelete}
      />
    </div>
  )
}
