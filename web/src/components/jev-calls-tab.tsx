import { useCallback, useEffect, useState } from "react"
import { RefreshCw } from "lucide-react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table"
import { StatusBadge } from "@/components/status-badge"
import { listJevCalls, type JevCall } from "@/lib/api"

export function JevCallsTab() {
  const [calls, setCalls] = useState<JevCall[]>([])

  const load = useCallback(async () => {
    try {
      setCalls(await listJevCalls(20))
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const inTotal = calls.reduce((a, c) => a + c.input_tokens, 0)
  const outTotal = calls.reduce((a, c) => a + c.output_tokens, 0)

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="secondary" onClick={load}>
          <RefreshCw /> Refresh
        </Button>
        <span className="flex-1" />
        <span className="text-xs text-muted-foreground tabular-nums">
          Σ in {inTotal} · out {outTotal} tokens (this view)
        </span>
      </div>

      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Time</TableHead>
              <TableHead>Model</TableHead>
              <TableHead>Endpoint</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>Latency</TableHead>
              <TableHead>In tok</TableHead>
              <TableHead>Out tok</TableHead>
              <TableHead>Error</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {calls.length === 0 && (
              <TableRow>
                <TableCell colSpan={8} className="py-8 text-center text-muted-foreground">
                  No AI API calls yet. They appear here whenever the gateway asks Jev for a judgment.
                </TableCell>
              </TableRow>
            )}
            {calls.map((c, i) => (
              <TableRow key={`${c.timestamp}-${i}`}>
                <TableCell className="tabular-nums">
                  {c.timestamp ? new Date(c.timestamp).toLocaleString() : ""}
                </TableCell>
                <TableCell className="font-mono text-xs">{c.model}</TableCell>
                <TableCell className="max-w-56 truncate font-mono text-xs" title={c.endpoint}>
                  {c.endpoint}
                </TableCell>
                <TableCell>
                  <StatusBadge
                    value={c.status}
                    className={
                      c.status === "ok"
                        ? "bg-emerald-100 text-emerald-800 hover:bg-emerald-100 dark:bg-emerald-950 dark:text-emerald-300"
                        : "bg-red-100 text-red-800 hover:bg-red-100 dark:bg-red-950 dark:text-red-300"
                    }
                  />
                  <span className="ml-1 text-xs text-muted-foreground tabular-nums">{c.http_status || ""}</span>
                </TableCell>
                <TableCell className="tabular-nums">{c.latency_ms}ms</TableCell>
                <TableCell className="tabular-nums">{c.input_tokens}</TableCell>
                <TableCell className="tabular-nums">{c.output_tokens}</TableCell>
                <TableCell className="max-w-72 truncate text-xs text-muted-foreground" title={c.error || undefined}>
                  {c.error || ""}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
      <p className="text-xs text-muted-foreground">
        In-memory ring (last 200 calls, this view shows 20). Token counts come from the API response;
        chat providers that omit usage report 0.
      </p>
    </div>
  )
}
