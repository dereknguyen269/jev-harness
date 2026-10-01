import { useCallback, useEffect, useState } from "react"
import { toast } from "sonner"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { getSettings, saveSettings } from "@/lib/api"
import { isAdminRole } from "@/lib/utils"

const MIN_TTL = 5
const MAX_TTL = 3600

export function SettingsTab({ role }: { role: string }) {
  const [ttl, setTtl] = useState("30")
  const [unavailable, setUnavailable] = useState(false)
  const [saving, setSaving] = useState(false)
  const admin = isAdminRole(role)

  const load = useCallback(async () => {
    try {
      const s = await getSettings()
      setTtl(String(s.approval_ttl_seconds))
      setUnavailable(false)
    } catch (err) {
      if ((err as { status?: number })?.status === 503) {
        setUnavailable(true)
      } else {
        toast.error(err instanceof Error ? err.message : String(err))
      }
    }
  }, [])

  useEffect(() => {
    load()
  }, [load])

  const save = async () => {
    const n = Number(ttl)
    if (!Number.isInteger(n) || n < MIN_TTL || n > MAX_TTL) {
      toast.error(`Enter a whole number of seconds (${MIN_TTL}–${MAX_TTL}).`)
      return
    }
    setSaving(true)
    try {
      const s = await saveSettings({ approval_ttl_seconds: n })
      setTtl(String(s.approval_ttl_seconds))
      toast.success(`Default approval timeout: ${s.approval_ttl_seconds}s (live)`)
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setSaving(false)
    }
  }

  if (unavailable) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        Settings need the SQLite store (YAML-only mode). The default approval timeout is 30s;
        override it with --approval-ttl / JEV_APPROVAL_TTL.
      </p>
    )
  }

  return (
    <div className="max-w-md space-y-4">
      <div className="space-y-1.5">
        <Label htmlFor="s-ttl">
          Default approval timeout <span className="font-normal text-muted-foreground">seconds, {MIN_TTL}–{MAX_TTL}</span>
        </Label>
        <Input
          id="s-ttl"
          type="number"
          min={MIN_TTL}
          max={MAX_TTL}
          value={ttl}
          disabled={!admin || saving}
          autoComplete="off"
          onChange={(e) => setTtl(e.target.value)}
        />
        <p className="text-xs text-muted-foreground">
          Applies to every approval_required decision without a per-rule timeout. Rules can override
          it with their own approval timeout (0 = this default). Saving applies immediately — no restart.
        </p>
      </div>
      {admin ? (
        <Button onClick={save} disabled={saving}>
          Save settings
        </Button>
      ) : (
        <p className="text-xs text-muted-foreground">read-only (admins can change settings)</p>
      )}
    </div>
  )
}
