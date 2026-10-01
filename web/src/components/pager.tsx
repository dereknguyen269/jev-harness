import { ChevronLeft, ChevronRight } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"

interface PagerProps {
  page: number
  pages: number
  total: number
  perPage: number
  perPageOptions: number[]
  onPage: (page: number) => void
  onPerPage: (perPage: number) => void
  id: string
}

export function Pager({ page, pages, total, perPage, perPageOptions, onPage, onPerPage, id }: PagerProps) {
  return (
    <div className="flex items-center gap-3 pt-2">
      <span className="text-xs text-muted-foreground" id={`${id}-page-info`}>
        {total ? `Page ${page} of ${pages} · ${total} rows` : ""}
      </span>
      <span className="flex-1" />
      <Button variant="outline" size="sm" disabled={page <= 1} onClick={() => onPage(page - 1)}>
        <ChevronLeft className="h-4 w-4" /> Prev
      </Button>
      <Button variant="outline" size="sm" disabled={page >= pages} onClick={() => onPage(page + 1)}>
        Next <ChevronRight className="h-4 w-4" />
      </Button>
      <Select value={String(perPage)} onValueChange={(v) => onPerPage(Number(v))}>
        <SelectTrigger className="w-20" aria-label="rows per page">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {perPageOptions.map((n) => (
            <SelectItem key={n} value={String(n)}>
              {n}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </div>
  )
}

export function paginate<T>(rows: T[], page: number, perPage: number): { slice: T[]; pages: number; page: number } {
  const pages = Math.max(1, Math.ceil(rows.length / perPage))
  const safe = Math.min(Math.max(1, page), pages)
  return { slice: rows.slice((safe - 1) * perPage, safe * perPage), pages, page: safe }
}
