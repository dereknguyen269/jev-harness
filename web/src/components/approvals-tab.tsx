import { useCallback, useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Pager } from "@/components/pager"
import { StatusBadge } from "@/components/status-badge"
import { decideApproval, listApprovalsPage, type Approval } from "@/lib/api"
import { canOperateRole } from "@/lib/utils"

function relTime(iso: string) {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000))
  if (s <= 0) return "expired"
  return `${s}s left`
}

function dateTime(iso: string) {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return "—"
  return d.toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  })
}

export function ApprovalsTab({ onPending, role }: { onPending: (n: number) => void; role: string }) {
  const [list, setList] = useState<Approval[]>([])
  const [page, setPage] = useState(1)
  const [pages, setPages] = useState(1)
  const [total, setTotal] = useState(0)
  const [perPage, setPerPage] = useState(25)
  const [auto, setAuto] = useState(true)

  const load = useCallback(async () => {
    try {
      const pg = await listApprovalsPage(page, perPage)
      setList(pg.approvals)
      setPages(pg.pages)
      setTotal(pg.total)
      onPending(pg.pending)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [onPending, page, perPage])

  useEffect(() => {
    load()
  }, [load])

  useEffect(() => {
    if (!auto) return
    const t = setInterval(load, 10000)
    return () => clearInterval(t)
  }, [auto, load])

  const decide = async (id: string, approve: boolean) => {
    try {
      await decideApproval(id, approve)
      toast.success(approve ? "Approved" : "Denied")
      load()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="secondary" onClick={load}>
          <RefreshCw /> Refresh
        </Button>
        <label className="flex items-center gap-2 text-sm">
          <Checkbox checked={auto} onCheckedChange={(v) => setAuto(v === true)} /> auto-refresh (10s)
        </label>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground">SQLite-backed: pending items survive restarts.</span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Created</TableHead>
              <TableHead>ID</TableHead>
              <TableHead>Tool</TableHead>
              <TableHead>Risk</TableHead>
              <TableHead>Reason</TableHead>
              <TableHead>Expires</TableHead>
              <TableHead>Status</TableHead>
              <TableHead className="text-right">Actions</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {list.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                  No approval requests.
                </TableCell>
              </TableRow>
            )}
            {list.map((a) => (
              <TableRow key={a.id}>
                <TableCell className="whitespace-nowrap tabular-nums text-xs">{dateTime(a.created_at)}</TableCell>
                <TableCell className="font-mono text-xs">{String(a.id).slice(0, 8)}…</TableCell>
                <TableCell>{a.tool}</TableCell>
                <TableCell className="tabular-nums">{a.risk.toFixed(2)}</TableCell>
                <TableCell>{a.reason}</TableCell>
                <TableCell className="tabular-nums">{relTime(a.expires_at)}</TableCell>
                <TableCell>
                  <StatusBadge value={a.status} />
                </TableCell>
                <TableCell className="whitespace-nowrap text-right">
                  {a.status === "pending" && canOperateRole(role) && (
                    <>
                      <Button size="sm" onClick={() => decide(a.id, true)}>
                        Approve
                      </Button>{" "}
                      <Button variant="destructive" size="sm" onClick={() => decide(a.id, false)}>
                        Deny
                      </Button>
                    </>
                  )}
                  {a.status === "pending" && !canOperateRole(role) && (
                    <span className="text-xs text-muted-foreground">read-only</span>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <Pager
        id="approvals"
        page={Math.min(page, pages)}
        pages={pages}
        total={total}
        perPage={perPage}
        perPageOptions={[10, 25, 50, 100]}
        onPage={setPage}
        onPerPage={(n) => {
          setPerPage(n)
          setPage(1)
        }}
      />
    </div>
  )
}
