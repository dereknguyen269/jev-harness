import { useState } from "react"
import { KeyRound } from "lucide-react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import { authStatus, getToken, login as loginApi, me, setToken, setUser, type Identity } from "@/lib/api"
import { SITE_NAME } from "@/lib/site"

export function Login({ onDone }: { onDone: (identity: Identity) => void }) {
  const [secret, setSecret] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  const submit = async () => {
    const s = secret.trim()
    if (!s) {
      setError("Enter your API key or the dashboard token.")
      return
    }
    setBusy(true)
    setError("")
    setToken(s)
    try {
      // Login resolves identity; only 401 means bad credentials.
      // Anything else (e.g. 503 YAML-only) still proves auth passed.
      const identity = await loginApi(s)
      setUser(identity)
      onDone(identity)
    } catch (err) {
      if ((err as { status?: number })?.status === 401) {
        setError("Invalid credentials. Try again.")
      } else {
        onDone({ name: "admin", role: "" })
      }
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="mx-auto flex min-h-[70vh] max-w-sm flex-col justify-center px-4">
      <div className="rounded-lg border bg-card p-6 shadow-sm">
        <div className="mb-4 flex items-center gap-2">
          <KeyRound className="h-5 w-5 text-primary" />
          <h2 className="text-lg font-semibold">{SITE_NAME}</h2>
        </div>
        <p className="mb-4 text-sm text-muted-foreground">
          Sign in with your API key, or paste the dashboard token
          (<code className="font-mono text-xs">--auth-token</code> / <code className="font-mono text-xs">JEV_AUTH_TOKEN</code>).
          Find your key in the Users tab after signing in as admin.
        </p>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="login-secret">
              API key or token <span className="text-destructive">*</span>
            </Label>
            <Input
              id="login-secret"
              type="password"
              value={secret}
              autoComplete="current-password"
              aria-invalid={!!error}
              onChange={(e) => {
                setSecret(e.target.value)
                setError("")
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter") submit()
              }}
            />
            {error && <p className="text-xs text-destructive">{error}</p>}
          </div>
          <Button className="w-full" onClick={submit} disabled={busy}>
            Sign in
          </Button>
        </div>
      </div>
    </div>
  )
}

// Resolve the gate state on load: open dashboard, valid session, or login.
// Only a 401 means "not authenticated"; other failures (backend down,
// YAML-only 503) render the app and let tabs show their own errors.
export async function probeAuth(): Promise<{ enabled: boolean; ok: boolean }> {
  let status: { auth: boolean }
  try {
    status = await authStatus()
  } catch {
    return { enabled: false, ok: true }
  }
  if (!status.auth) return { enabled: false, ok: true }
  if (!getToken()) return { enabled: true, ok: false }
  try {
    await me()
    return { enabled: true, ok: true }
  } catch (err) {
    if ((err as { status?: number })?.status === 401) return { enabled: true, ok: false }
    return { enabled: true, ok: true }
  }
}
