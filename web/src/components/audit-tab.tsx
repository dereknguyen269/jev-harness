import { useCallback, useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { Pager, paginate } from "@/components/pager"
import { StatusBadge } from "@/components/status-badge"
import { getStats, listAudit, type AuditEvent, type Stats } from "@/lib/api"

export function AuditTab() {
  const [stats, setStats] = useState<Stats>({})
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [filter, setFilter] = useState("")
  const [page, setPage] = useState(1)
  const [perPage, setPerPage] = useState(20)

  const load = useCallback(async () => {
    try {
      const [s, e] = await Promise.all([getStats(), listAudit(filter || undefined)])
      setStats(s)
      setEvents([...e].reverse()) // newest first
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [filter])

  useEffect(() => {
    load()
  }, [load])

  const entries = Object.entries(stats).filter(([k]) => k !== "total")
  const total = entries.reduce((a, [, b]) => a + b, 0)
  const cards: [string, number][] = [["total", total], ...entries]

  const { slice, pages, page: safePage } = paginate(events, page, perPage)

  return (
    <div className="space-y-3">
      <div className="flex gap-2 sm:gap-3">
        {cards.map(([k, v]) => (
          <div key={k} className="min-w-0 flex-1 rounded-lg border bg-card p-3 shadow-sm sm:p-4">
            <div className="text-xl font-bold tabular-nums sm:text-2xl">{v}</div>
            <div className="truncate text-xs text-muted-foreground" title={k}>
              {k}
            </div>
            <div className="mt-2 h-1 overflow-hidden rounded-full bg-muted">
              <div className="h-full bg-primary" style={{ width: `${total ? Math.round((v / total) * 100) : 0}%` }} />
            </div>
          </div>
        ))}
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <Select
          value={filter || "all"}
          onValueChange={(v) => {
            setFilter(v === "all" ? "" : v)
            setPage(1)
          }}
        >
          <SelectTrigger className="w-52" aria-label="filter by decision">
            <SelectValue placeholder="all decisions" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">all decisions</SelectItem>
            <SelectItem value="allow">allow</SelectItem>
            <SelectItem value="block">block</SelectItem>
            <SelectItem value="approval_required">approval_required</SelectItem>
          </SelectContent>
        </Select>
        <Button
          variant="secondary"
          onClick={() => {
            setPage(1)
            load()
          }}
        >
          <RefreshCw /> Refresh
        </Button>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Agent</TableHead>
              <TableHead>Tool</TableHead>
              <TableHead>Detail</TableHead>
              <TableHead>Decision</TableHead>
              <TableHead>Risk</TableHead>
              <TableHead>Reason</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {slice.length === 0 && (
              <TableRow>
                <TableCell colSpan={7} className="py-8 text-center text-muted-foreground">
                  No audit events.
                </TableCell>
              </TableRow>
            )}
            {slice.map((e) => {
              const detail = e.command || e.path || e.resource || ""
              return (
                <TableRow key={e.id}>
                  <TableCell className="tabular-nums">
                    {e.timestamp ? new Date(e.timestamp).toLocaleString() : ""}
                  </TableCell>
                  <TableCell>{e.agent}</TableCell>
                  <TableCell>{e.tool}</TableCell>
                  <TableCell
                    className="max-w-72 truncate font-mono text-xs"
                    title={detail || undefined}
                  >
                    {detail}
                  </TableCell>
                  <TableCell>
                    <StatusBadge value={e.final_decision} />
                  </TableCell>
                  <TableCell className="tabular-nums">{e.risk.toFixed(2)}</TableCell>
                  <TableCell>{e.reason_code || ""}</TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      </div>

      <Pager
        id="audit"
        page={safePage}
        pages={pages}
        total={events.length}
        perPage={perPage}
        perPageOptions={[10, 20, 50, 100]}
        onPage={setPage}
        onPerPage={(n) => {
          setPerPage(n)
          setPage(1)
        }}
      />
    </div>
  )
}
