import { useCallback, useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { StatusBadge } from "@/components/status-badge"
import { decideApproval, listApprovals, type Approval } from "@/lib/api"
import { canOperateRole } from "@/lib/utils"

function relTime(iso: string) {
  const s = Math.max(0, Math.round((new Date(iso).getTime() - Date.now()) / 1000))
  if (s <= 0) return "expired"
  return `${s}s left`
}

export function ApprovalsTab({ onPending, role }: { onPending: (n: number) => void; role: string }) {
  const [list, setList] = useState<Approval[]>([])
  const [auto, setAuto] = useState(true)

  const load = useCallback(async () => {
    try {
      const l = await listApprovals()
      setList(l)
      onPending(l.filter((a) => a.status === "pending").length)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [onPending])

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
        <span className="text-xs text-muted-foreground">In-memory: pending items vanish on restart.</span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
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
                <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                  No approval requests.
                </TableCell>
              </TableRow>
            )}
            {list.map((a) => (
              <TableRow key={a.id}>
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
    </div>
  )
}
