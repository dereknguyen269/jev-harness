import { useCallback, useEffect, useState } from "react"
import { CircleHelp, CircleUser, Laptop, LogOut, Moon, Sun } from "lucide-react"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"
import { Toaster } from "@/components/ui/sonner"
import { ApprovalsTab } from "@/components/approvals-tab"
import { AuditTab } from "@/components/audit-tab"
import { CategoriesTab } from "@/components/categories-tab"
import { ConfirmDialog } from "@/components/confirm-dialog"
import { GroupsTab } from "@/components/groups-tab"
import { JevCallsTab } from "@/components/jev-calls-tab"
import { Login, probeAuth } from "@/components/login"
import { OnboardingTour, markOnboarded, wasOnboarded } from "@/components/onboarding-tour"
import { RulesTab } from "@/components/rules-tab"
import { UsersTab } from "@/components/users-tab"
import { useTheme } from "@/hooks/use-theme"
import { clearToken, clearUser, getHealth, getUser, me, type Identity } from "@/lib/api"
import { SITE_NAME } from "@/lib/site"
import { escHtml } from "@/lib/utils"

function ThemeButton() {
  const { theme, cycle } = useTheme()
  const Icon = theme === "dark" ? Moon : theme === "light" ? Sun : Laptop
  return (
    <Button variant="ghost" size="sm" onClick={cycle} title={`theme: ${theme} (click to cycle)`}>
      <Icon /> {theme}
    </Button>
  )
}

export default function App() {
  const [db, setDb] = useState<boolean | null>(null)
  const [ruleCount, setRuleCount] = useState(0)
  const [health, setHealth] = useState("–")
  const [pending, setPending] = useState(0)
  const [tourOpen, setTourOpen] = useState(false)
  const [signoutOpen, setSignoutOpen] = useState(false)
  const [auth, setAuth] = useState<"loading" | "login" | "ok">("loading")
  const [authEnabled, setAuthEnabled] = useState(false)
  const [user, setUserState] = useState<Identity | null>(null)

  useEffect(() => {
    probeAuth().then(({ enabled, ok }) => {
      setAuthEnabled(enabled)
      setAuth(ok ? "ok" : "login")
      if (ok && enabled) {
        me()
          .then(setUserState)
          .catch(() => setUserState(getUser()))
      }
    })
  }, [])

  useEffect(() => {
    getHealth()
      .then((h) => setHealth(`v${h.version || "?"} · jev ${h.jev ? "on" : "off"}`))
      .catch(() => setHealth("unreachable"))
  }, [])

  // First visit: open the onboarding tour once.
  useEffect(() => {
    if (!wasOnboarded()) setTourOpen(true)
  }, [])

  const handleTour = (open: boolean) => {
    setTourOpen(open)
    if (!open) markOnboarded()
  }

  const onCount = useCallback((isDb: boolean, n: number) => {
    setDb(isDb)
    setRuleCount(n)
  }, [])

  const onPending = useCallback((n: number) => setPending(n), [])

  // Auth disabled means the backend allows everything: show the full UI.
  const effectiveRole = authEnabled ? (user?.role ?? "") : "admin"

  return (
    <div className="min-h-screen bg-background text-foreground">
      <header className="sticky top-0 z-10 border-b bg-background/95 backdrop-blur">
        <div className="mx-auto flex max-w-6xl flex-wrap items-center gap-2 px-4 py-2">
          <h1 className="text-base font-bold">{SITE_NAME}</h1>
          <Badge variant="secondary">
            <span
              className={`mr-1 inline-block h-2 w-2 rounded-full ${db ? "bg-emerald-500" : "bg-amber-500"}`}
            />
            {db === null ? "connecting…" : db ? `SQLite · ${ruleCount} rules` : "YAML-only"}
          </Badge>
          <Badge variant="secondary">
            <span className="mr-1 inline-block h-2 w-2 rounded-full bg-sky-500" />
            {health}
          </Badge>
          <span className="flex-1" />
          {authEnabled && user && (
            <Badge variant="secondary" title={`signed in as ${user.name} (${user.role || "no role"})`}>
              <CircleUser className="mr-1 h-3 w-3" />
              {user.name}
            </Badge>
          )}
          {auth === "ok" && (
            <Button variant="ghost" size="sm" onClick={() => setTourOpen(true)} title="replay the onboarding tour">
              <CircleHelp /> Guide
            </Button>
          )}
          {auth === "ok" && authEnabled && (
            <Button variant="ghost" size="sm" title="sign out" onClick={() => setSignoutOpen(true)}>
              <LogOut /> Sign out
            </Button>
          )}
          <ThemeButton />
        </div>
      </header>

      <main className="mx-auto max-w-6xl px-4 py-4">
        {auth === "loading" && <p className="py-8 text-center text-sm text-muted-foreground">Connecting…</p>}
        {auth === "login" && (
          <Login
            onDone={(identity) => {
              setUserState(identity)
              setAuth("ok")
            }}
          />
        )}
        {auth === "ok" && (
        <Tabs defaultValue="rules">
          <TabsList className="mb-4 flex-wrap">
            <TabsTrigger value="rules">Rules</TabsTrigger>
            <TabsTrigger value="groups">Groups</TabsTrigger>
            <TabsTrigger value="categories">Categories</TabsTrigger>
            <TabsTrigger value="users">Users</TabsTrigger>
            <TabsTrigger value="approvals">
              Approvals
              {pending > 0 && (
                <Badge variant="secondary" className="ml-1.5">
                  {pending}
                </Badge>
              )}
            </TabsTrigger>
            <TabsTrigger value="audit">Audit</TabsTrigger>
            <TabsTrigger value="jev">Jev API</TabsTrigger>
          </TabsList>
          <TabsContent value="rules">
            <RulesTab onCount={onCount} role={effectiveRole} />
          </TabsContent>
          <TabsContent value="groups">
            <GroupsTab role={effectiveRole} />
          </TabsContent>
          <TabsContent value="categories">
            <CategoriesTab role={effectiveRole} />
          </TabsContent>
          <TabsContent value="users">
            <UsersTab role={effectiveRole} />
          </TabsContent>
          <TabsContent value="approvals">
            <ApprovalsTab onPending={onPending} role={effectiveRole} />
          </TabsContent>
          <TabsContent value="audit">
            <AuditTab />
          </TabsContent>
          <TabsContent value="jev">
            <JevCallsTab />
          </TabsContent>
        </Tabs>
        )}
      </main>

      <Toaster />
      <OnboardingTour open={tourOpen} onOpenChange={handleTour} />
      <ConfirmDialog
        open={signoutOpen}
        onOpenChange={setSignoutOpen}
        title="Sign out"
        lines={user ? [`Signed in as <b>${escHtml(user.name)}</b>.`] : ["End this session?"]}
        confirmLabel="Sign out"
        onConfirm={() => {
          clearToken()
          clearUser()
          setUserState(null)
          setSignoutOpen(false)
          setAuth("login")
        }}
      />
    </div>
  )
}
