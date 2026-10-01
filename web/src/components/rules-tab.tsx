import { useCallback, useEffect, useMemo, useState } from "react"
import { Pencil, Plus, RefreshCw, Trash2 } from "lucide-react"
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
import { Textarea } from "@/components/ui/textarea"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { Pager, paginate } from "@/components/pager"
import { StatusBadge } from "@/components/status-badge"
import { canOperateRole, escHtml, isAdminRole } from "@/lib/utils"
import { deleteRule, getSettings, listCategories, listGroups, listRules, reloadPolicy, reseedPolicy, saveRule, type Category, type Group, type Rule, type RuleInput } from "@/lib/api"

const BLANK: RuleInput = {
  id: "",
  tool: "",
  pattern: "",
  action: "block",
  priority: 50,
  group: "",
  category: "",
  business: "",
  task: "",
  description: "",
  approval_timeout: 0,
}

const ACTIONS = ["allow", "block", "approval_required"]

// Tools the engine can match. "*" (and blank) match any tool; anything
// else falls back to a free-text custom value.
const KNOWN_TOOLS = [
  "*",
  "terminal",
  "read_file",
  "write_file",
  "delete_file",
  "patch",
  "network",
  "browser_navigate",
  "browser_extract",
]
const toolLabel = (t: string) => (t === "*" ? "Any tool (*)" : t)

// Fallback groups mirror configs/policy.yaml (used when the store is
// unreachable). The dropdown merges these with groups already stored in the
// DB, so custom groups persist as options. Adopt restructured defaults on an
// existing DB via the Reseed button or `jev-guard policy reseed`.
const DEFAULT_GROUPS = [
  "critical-safety",
  "privileged-ops",
  "secrets",
  "code-allow",
  "patch-allow",
  "browser",
  "git",
  "system-read",
]

// Textarea that grows with content (long regexes) up to 320px, then scrolls.
function GrowTextarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  const grow = (el: HTMLTextAreaElement | null) => {
    if (!el) return
    el.style.height = "auto"
    el.style.height = Math.min(el.scrollHeight, 320) + "px"
  }
  return (
    <Textarea
      {...props}
      ref={grow}
      onChange={(e) => {
        grow(e.target)
        props.onChange?.(e)
      }}
    />
  )
}

interface RuleDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  initial: RuleInput
  isEdit: boolean
  groups: Group[]
  categories: Category[]
  defaultTtl: number
  onSaved: () => void
}

function RuleDialog({ open, onOpenChange, initial, isEdit, groups, categories, defaultTtl, onSaved }: RuleDialogProps) {
  const [form, setForm] = useState<RuleInput>(initial)
  const [step, setStep] = useState<"form" | "review">("form")
  const [errors, setErrors] = useState<Record<string, string>>({})
  const [saving, setSaving] = useState(false)
  const [customTool, setCustomTool] = useState(false)
  const [customValue, setCustomValue] = useState("")
  const [customGroup, setCustomGroup] = useState(false)
  const [customGroupValue, setCustomGroupValue] = useState("")
  const [customCategory, setCustomCategory] = useState(false)
  const [customCategoryValue, setCustomCategoryValue] = useState("")

  useEffect(() => {
    if (open) {
      setForm(initial)
      setStep("form")
      setErrors({})
      const knownTool = initial.tool === "" || KNOWN_TOOLS.includes(initial.tool)
      setCustomTool(!knownTool)
      setCustomValue(knownTool ? "" : initial.tool)
      const knownGroup = initial.group === "" || groups.some((g) => g.name === initial.group)
      setCustomGroup(!knownGroup)
      setCustomGroupValue(knownGroup ? "" : initial.group)
      const knownCategory =
        initial.category === "" || categories.some((c) => c.name === initial.category)
      setCustomCategory(!knownCategory)
      setCustomCategoryValue(knownCategory ? "" : initial.category)
    }
  }, [open, initial])

  const set = (k: keyof RuleInput, v: string | number) => {
    setForm((f) => ({ ...f, [k]: v }))
    setErrors((e) => ({ ...e, [k]: "" }))
  }

  const validate = () => {
    const e: Record<string, string> = {}
    if (!form.pattern.trim()) e.pattern = "Pattern is required."
    if (!ACTIONS.includes(form.action)) e.action = "Pick a valid action."
    if (customTool && !customValue.trim()) e.tool = "Enter a custom tool name or pick from the list."
    if (customGroup && !customGroupValue.trim()) e.group = "Enter a custom group name or pick from the list."
    if (customCategory && !customCategoryValue.trim())
      e.category = "Enter a custom category name or pick from the list."
    const timeout = Number(form.approval_timeout) || 0
    if (form.action === "approval_required") {
      if (timeout !== 0 && (timeout < 5 || timeout > 3600)) e.approval_timeout = "Use 0 (default) or 5–3600 seconds."
    } else if (timeout !== 0) {
      e.approval_timeout = "Only approval_required rules use a timeout."
    }
    setErrors(e)
    return Object.keys(e).length === 0
  }

  const review = () => {
    if (validate()) setStep("review")
  }

  // Effective tool/group/category: custom free-text wins when custom mode is on.
  const effectiveTool = customTool ? customValue.trim() : form.tool
  const effectiveGroup = customGroup ? customGroupValue.trim() : form.group
  const effectiveCategory = customCategory ? customCategoryValue.trim() : form.category

  const submit = async () => {
    setSaving(true)
    try {
      const saved = await saveRule({ ...form, tool: effectiveTool, group: effectiveGroup, category: effectiveCategory })
      toast.success(`Saved ${saved.id} (engine reloaded)`)
      onOpenChange(false)
      onSaved()
    } catch (err) {
      const msg = err instanceof Error ? err.message : String(err)
      if (/pattern|regex/i.test(msg)) {
        setStep("form")
        setErrors({ pattern: msg })
      } else if (/action/i.test(msg)) {
        setStep("form")
        setErrors({ action: msg })
      } else {
        toast.error(msg)
      }
    } finally {
      setSaving(false)
    }
  }

  const field = (id: string, label: string, required: boolean, hint: string, control: React.ReactNode, err?: string) => (
    <div className="space-y-1.5">
      <Label htmlFor={id}>
        {label} {required && <span className="text-destructive">*</span>}{" "}
        <span className="font-normal text-muted-foreground">{hint}</span>
      </Label>
      {control}
      {err && <p className="text-xs text-destructive">{err}</p>}
    </div>
  )

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{isEdit ? "Edit rule" : "New rule"}</DialogTitle>
          <DialogDescription>
            {step === "review"
              ? "Review before saving. The live engine reloads immediately."
              : "First match wins (file order). Saving reloads the live engine."}
          </DialogDescription>
        </DialogHeader>
        {step === "form" ? (
          <div className="grid grid-cols-2 gap-4">
            {field("f-id", "ID", false, "blank = new", <Input id="f-id" value={form.id} readOnly={isEdit} placeholder="auto" autoComplete="off" onChange={(e) => set("id", e.target.value)} />)}
            {field(customTool ? "f-tool-custom" : "f-tool", "Tool", false, customTool ? "custom value" : "match scope", customTool ? (
              <div className="space-y-1.5">
                <Input
                  id="f-tool-custom"
                  value={customValue}
                  placeholder="my_tool"
                  autoComplete="off"
                  aria-invalid={!!errors.tool}
                  onChange={(e) => {
                    setCustomValue(e.target.value)
                    setErrors((p) => ({ ...p, tool: "" }))
                  }}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setCustomTool(false)
                    setCustomValue("")
                    setErrors((p) => ({ ...p, tool: "" }))
                  }}
                >
                  ← back to list
                </Button>
              </div>
            ) : (
              <Select
                value={form.tool === "" ? "*" : form.tool}
                onValueChange={(v) => {
                  if (v === "__custom__") {
                    setCustomTool(true)
                    setCustomValue("")
                  } else {
                    set("tool", v)
                  }
                  setErrors((e) => ({ ...e, tool: "" }))
                }}
              >
                <SelectTrigger id="f-tool" aria-invalid={!!errors.tool}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {KNOWN_TOOLS.map((t) => (
                    <SelectItem key={t} value={t}>
                      {toolLabel(t)}
                    </SelectItem>
                  ))}
                  <SelectItem value="__custom__">Custom…</SelectItem>
                </SelectContent>
              </Select>
            ), errors.tool)}
            <div className="col-span-2">
              {field(
                "f-pattern",
                "Pattern",
                true,
                "Go regexp",
                <GrowTextarea
                  id="f-pattern"
                  value={form.pattern}
                  rows={6}
                  placeholder="rm\s+-rf\s+/"
                  autoComplete="off"
                  aria-required="true"
                  aria-invalid={!!errors.pattern}
                  className="font-mono text-xs"
                  onChange={(e) => set("pattern", e.target.value)}
                />,
                errors.pattern,
              )}
            </div>
            {field(
              "f-action",
              "Action",
              false,
              "",
              <Select value={form.action} onValueChange={(v) => {
                set("action", v)
                if (v !== "approval_required") set("approval_timeout", 0)
              }}>
                <SelectTrigger id="f-action" aria-invalid={!!errors.action}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="block">block</SelectItem>
                  <SelectItem value="approval_required">approval_required</SelectItem>
                  <SelectItem value="allow">allow</SelectItem>
                </SelectContent>
              </Select>,
              errors.action,
            )}
            {field("f-priority", "Priority", false, "", <Input id="f-priority" type="number" value={form.priority} onChange={(e) => set("priority", Number(e.target.value) || 0)} />)}
            {field(
              "f-timeout",
              "Approval timeout",
              false,
              form.action === "approval_required" ? `seconds, 0 = default (${defaultTtl}s)` : "only for approval_required",
              <Input
                id="f-timeout"
                type="number"
                min={0}
                max={3600}
                value={form.approval_timeout || ""}
                placeholder={`default (${defaultTtl}s)`}
                disabled={form.action !== "approval_required"}
                aria-invalid={!!errors.approval_timeout}
                onChange={(e) => set("approval_timeout", Number(e.target.value) || 0)}
              />,
              errors.approval_timeout,
            )}
            {field(customGroup ? "f-group-custom" : "f-group", "Group", false, customGroup ? "custom value" : "blank = ungrouped", customGroup ? (
              <div className="space-y-1.5">
                <Input
                  id="f-group-custom"
                  value={customGroupValue}
                  placeholder="my-group"
                  autoComplete="off"
                  aria-invalid={!!errors.group}
                  onChange={(e) => {
                    setCustomGroupValue(e.target.value)
                    setErrors((p) => ({ ...p, group: "" }))
                  }}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setCustomGroup(false)
                    setCustomGroupValue("")
                    setErrors((p) => ({ ...p, group: "" }))
                  }}
                >
                  ← back to list
                </Button>
              </div>
            ) : (
              <Select
                value={form.group === "" ? "__none__" : form.group}
                onValueChange={(v) => {
                  if (v === "__custom__") {
                    setCustomGroup(true)
                    setCustomGroupValue("")
                  } else {
                    set("group", v === "__none__" ? "" : v)
                  }
                  setErrors((e) => ({ ...e, group: "" }))
                }}
              >
                <SelectTrigger id="f-group" aria-invalid={!!errors.group}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">(none — always active)</SelectItem>
                  {groups.map((g) => (
                    <SelectItem key={g.name} value={g.name}>
                      {g.description ? `${g.name} — ${g.description}` : g.name}
                    </SelectItem>
                  ))}
                  <SelectItem value="__custom__">Custom…</SelectItem>
                </SelectContent>
              </Select>
            ), errors.group)}
            {field(customCategory ? "f-category-custom" : "f-category", "Category", false, customCategory ? "custom value" : "blank = match all", customCategory ? (
              <div className="space-y-1.5">
                <Input
                  id="f-category-custom"
                  value={customCategoryValue}
                  placeholder="safety"
                  autoComplete="off"
                  aria-invalid={!!errors.category}
                  onChange={(e) => {
                    setCustomCategoryValue(e.target.value)
                    setErrors((p) => ({ ...p, category: "" }))
                  }}
                />
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setCustomCategory(false)
                    setCustomCategoryValue("")
                    setErrors((p) => ({ ...p, category: "" }))
                  }}
                >
                  ← back to list
                </Button>
              </div>
            ) : (
              <Select
                value={form.category === "" ? "__none__" : form.category}
                onValueChange={(v) => {
                  if (v === "__custom__") {
                    setCustomCategory(true)
                    setCustomCategoryValue("")
                  } else {
                    set("category", v === "__none__" ? "" : v)
                  }
                  setErrors((e) => ({ ...e, category: "" }))
                }}
              >
                <SelectTrigger id="f-category" aria-invalid={!!errors.category}>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="__none__">(none — match all)</SelectItem>
                  {categories.map((c) => (
                    <SelectItem key={c.name} value={c.name}>
                      {c.description ? `${c.name} — ${c.description}` : c.name}
                    </SelectItem>
                  ))}
                  <SelectItem value="__custom__">Custom…</SelectItem>
                </SelectContent>
              </Select>
            ), errors.category)}
            {field("f-business", "Business", false, "", <Input id="f-business" value={form.business} autoComplete="off" onChange={(e) => set("business", e.target.value)} />)}
            {field("f-task", "Task", false, "", <Input id="f-task" value={form.task} autoComplete="off" onChange={(e) => set("task", e.target.value)} />)}
            <div className="col-span-2">
              {field("f-description", "Description", false, "", <Textarea id="f-description" value={form.description} rows={2} autoComplete="off" onChange={(e) => set("description", e.target.value)} />)}
            </div>
          </div>
        ) : (
          <div className="space-y-2">
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              ID: <code className="font-mono text-xs">{form.id || "(auto id)"}</code>
            </p>
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              Tool <b>{effectiveTool || "*"}</b> · action <b>{form.action}</b> · priority <b>{form.priority}</b>
              {form.action === "approval_required" && (
                <> · timeout <b>{form.approval_timeout ? `${form.approval_timeout}s` : `default (${defaultTtl}s)`}</b></>
              )}
              {effectiveGroup && (
                <>
                  {" "}· group <b>{effectiveGroup}</b>
                </>
              )}
              {effectiveCategory && (
                <>
                  {" "}· category <b>{effectiveCategory}</b>
                </>
              )}
            </p>
            <p className="rounded-md border bg-muted/50 px-3 py-2 text-sm">
              Pattern: <code className="font-mono text-xs break-all">{form.pattern}</code>
            </p>
            <p className="text-sm text-muted-foreground">The live engine reloads immediately.</p>
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

export function RulesTab({ onCount, role }: { onCount: (db: boolean, n: number) => void; role: string }) {
  const [rules, setRules] = useState<Rule[]>([])
  const [defaultTtl, setDefaultTtl] = useState(30)
  const [definedGroups, setDefinedGroups] = useState<Group[]>([])
  const [definedCategories, setDefinedCategories] = useState<Category[]>([])
  const [query, setQuery] = useState("")
  const [actionFilter, setActionFilter] = useState("")
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(15)
  const [dialog, setDialog] = useState<{ open: boolean; initial: RuleInput; isEdit: boolean }>({
    open: false,
    initial: BLANK,
    isEdit: false,
  })
  const [deleting, setDeleting] = useState<Rule | null>(null)
  const [reseedConfirm, setReseedConfirm] = useState(false)

  const load = useCallback(async () => {
    try {
      const [r, g, c] = await Promise.all([
        listRules(),
        listGroups().catch(() => [] as Group[]),
        listCategories().catch(() => [] as Category[]),
      ])
      setRules(r)
      setDefinedGroups(g)
      setDefinedCategories(c)
      onCount(true, r.length)
    } catch (err) {
      onCount(false, 0)
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [onCount])

  useEffect(() => {
    load()
  }, [load])

  // Default approval timeout for the rule dialog hint (falls back to 30s).
  useEffect(() => {
    getSettings()
      .then((s) => setDefaultTtl(s.approval_ttl_seconds))
      .catch(() => {})
  }, [])

  const rows = useMemo(() => {
    const q = query.trim().toLowerCase()
    return rules.filter((r) => {
      if (actionFilter && r.action !== actionFilter) return false
      if (!q) return true
      return [r.id, r.tool, r.pattern, r.group, r.category, r.business, r.task, r.description].some((v) =>
        String(v || "").toLowerCase().includes(q),
      )
    })
  }, [rules, query, actionFilter])

  // Dropdown options: policy defaults first, then defined groups (with
  // descriptions), then any rule groups present in neither. Falls back to
  // pure defaults when the store is unreachable (YAML-only mode).
  const groups = useMemo(() => {
    const seen = new Set<string>()
    const out: Group[] = []
    const push = (name: string, description = "") => {
      if (name && !seen.has(name)) {
        seen.add(name)
        out.push({ name, description })
      }
    }
    DEFAULT_GROUPS.forEach((g) => push(g))
    definedGroups.forEach((g) => push(g.name, g.description))
    const extra: string[] = []
    for (const r of rules) {
      if (r.group && !seen.has(r.group)) {
        seen.add(r.group)
        extra.push(r.group)
      }
    }
    extra.sort().forEach((g) => push(g))
    return out
  }, [rules, definedGroups])

  // Category options: defined first, then any rule categories present in
  // neither definitions nor YAML seeds. No policy defaults exist for
  // categories; YAML-only mode offers (none) + custom only.
  const categories = useMemo(() => {
    const seen = new Set<string>()
    const out: Category[] = []
    const push = (name: string, description = "") => {
      if (name && !seen.has(name)) {
        seen.add(name)
        out.push({ name, description })
      }
    }
    definedCategories.forEach((c) => push(c.name, c.description))
    const extra: string[] = []
    for (const r of rules) {
      if (r.category && !seen.has(r.category)) {
        seen.add(r.category)
        extra.push(r.category)
      }
    }
    extra.sort().forEach((c) => push(c))
    return out
  }, [rules, definedCategories])

  const { slice, pages, page: safePage } = paginate(rows, page, perPage)

  const doDelete = async () => {
    if (!deleting) return
    try {
      await deleteRule(deleting.id)
      toast.success("Rule deleted")
      setDeleting(null)
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Input
          type="search"
          placeholder="Filter id, tool, pattern, group…"
          aria-label="filter rules"
          className="min-w-44 flex-1"
          value={query}
          onChange={(e) => {
            setQuery(e.target.value)
            setPage(1)
          }}
        />
        <Select
          value={actionFilter || "all"}
          onValueChange={(v) => {
            setActionFilter(v === "all" ? "" : v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-44" aria-label="filter by action">
            <SelectValue placeholder="all actions" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">all actions</SelectItem>
            <SelectItem value="allow">allow</SelectItem>
            <SelectItem value="block">block</SelectItem>
            <SelectItem value="approval_required">approval_required</SelectItem>
          </SelectContent>
        </Select>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">{rules.length} rules</span>
        {isAdminRole(role) && (
          <Button onClick={() => setDialog({ open: true, initial: BLANK, isEdit: false })}>
            <Plus /> New rule
          </Button>
        )}
        {canOperateRole(role) && (
          <Button
            variant="secondary"
            onClick={async () => {
              try {
                const r = await reloadPolicy()
                toast.success(`Reloaded ${r.rules} rules`)
                load()
              } catch (err) {
                toast.error(err instanceof Error ? err.message : String(err))
              }
            }}
          >
            <RefreshCw /> Reload policy
          </Button>
        )}
        {isAdminRole(role) && (
          <Button variant="secondary" onClick={() => setReseedConfirm(true)}>
            <RefreshCw /> Reseed defaults
          </Button>
        )}
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>ID</TableHead>
              <TableHead>Tool</TableHead>
              <TableHead>Pattern</TableHead>
              <TableHead>Action</TableHead>
              <TableHead>Prio</TableHead>
              <TableHead>Group</TableHead>
              <TableHead>Scope</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {slice.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                  {rules.length ? "No rules match the filter." : "No rules yet."}
                </TableCell>
              </TableRow>
            )}
            {slice.map((r) => (
              <TableRow key={r.id}>
                <TableCell className="font-mono text-xs">{r.id}</TableCell>
                <TableCell>{r.tool}</TableCell>
                <TableCell>
                  <code className="font-mono text-xs break-all">{r.pattern}</code>
                </TableCell>
                <TableCell>
                  <StatusBadge value={r.action} />
                  {r.action === "approval_required" && !!r.approval_timeout && (
                    <div className="text-xs text-muted-foreground tabular-nums">{r.approval_timeout}s timeout</div>
                  )}
                </TableCell>
                <TableCell className="tabular-nums">{r.priority}</TableCell>
                <TableCell>{r.group}</TableCell>
                <TableCell>{[r.category, r.business, r.task].filter(Boolean).join(" / ")}</TableCell>
                <TableCell className="whitespace-nowrap text-right">
                  {isAdminRole(role) ? (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                    aria-label={`edit rule ${r.id}`}
                    onClick={() =>
                      setDialog({
                        open: true,
                        initial: {
                          id: r.id,
                          tool: r.tool,
                          pattern: r.pattern,
                          action: r.action,
                          priority: r.priority,
                          group: r.group,
                          category: r.category,
                          business: r.business,
                          task: r.task,
                          description: r.description,
                          approval_timeout: r.approval_timeout || 0,
                        },
                        isEdit: true,
                      })
                    }
                  >
                    <Pencil /> Edit
                  </Button>{" "}
                  <Button
                    variant="destructive"
                    size="sm"
                    aria-label={`delete rule ${r.id}`}
                    onClick={() => setDeleting(r)}
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

      <Pager
        id="rules"
        page={safePage}
        pages={pages}
        total={rows.length}
        perPage={perPage}
        perPageOptions={[10, 15, 25, 50]}
        onPage={setPage}
        onPerPage={(n) => {
          setPerPage(n)
          setPage(1)
        }}
      />

      <RuleDialog
        open={dialog.open}
        onOpenChange={(o) => setDialog((d) => ({ ...d, open: o }))}
        initial={dialog.initial}
        isEdit={dialog.isEdit}
        groups={groups}
        categories={categories}
        defaultTtl={defaultTtl}
        onSaved={load}
      />

      <ConfirmDialog
        open={reseedConfirm}
        onOpenChange={setReseedConfirm}
        title="Reseed default policy"
        lines={[
          "Merge bundled YAML defaults into the DB (adopts restructures).",
          "Custom rules are kept; stale yaml defaults are pruned.",
          "The live engine reloads immediately.",
        ]}
        confirmLabel="Reseed"
        onConfirm={async () => {
          try {
            const r = await reseedPolicy("merge")
            toast.success(`Reseeded ${r.rules} rules, pruned ${r.pruned}`)
            setReseedConfirm(false)
            load()
          } catch (err) {
            toast.error(err instanceof Error ? err.message : String(err))
          }
        }}
      />

      <ConfirmDialog
        open={deleting !== null}
        onOpenChange={(o) => !o && setDeleting(null)}
        title="Delete rule"
        lines={
          deleting
            ? [
                `ID: <code class="font-mono text-xs">${escHtml(deleting.id)}</code>`,
                "The live engine reloads immediately.",
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