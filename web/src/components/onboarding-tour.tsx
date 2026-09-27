import { useEffect, useState } from "react"
import { Hourglass, ListChecks, Rocket, ScrollText, ShieldCheck, Users } from "lucide-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"
import { SITE_NAME } from "@/lib/site"

const FLAG = "jev-onboarded"

interface Step {
  icon: React.ReactNode
  title: string
  body: React.ReactNode
}

const STEPS: Step[] = [
  {
    icon: <ShieldCheck className="h-8 w-8 text-primary" />,
    title: `Welcome to ${SITE_NAME}`,
    body: (
      <>
        <p>
          {SITE_NAME} sits between your AI agents and their tools. Every tool call is checked against your
          policy and gets one verdict: <b>allow</b>, <b>block</b>, or <b>approval required</b>.
        </p>
        <p className="text-muted-foreground">
          The header badges show the live state: policy source (SQLite or YAML-only) and gateway version.
          This tour takes 30 seconds.
        </p>
      </>
    ),
  },
  {
    icon: <ListChecks className="h-8 w-8 text-primary" />,
    title: "Rules are the policy",
    body: (
      <>
        <p>
          Rules match tool calls by <b>tool + regex pattern</b>, first match wins. Editing a rule reloads
          the live engine immediately — no restart.
        </p>
        <p className="text-muted-foreground">
          Use the search box and action filter to find rules. Long patterns (like the file-extension
          allowlists) expand as you type.
        </p>
      </>
    ),
  },
  {
    icon: <Users className="h-8 w-8 text-primary" />,
    title: "Users tag audit rows",
    body: (
      <>
        <p>
          Users sign in with their API key. Roles gate access: <b>viewers</b> read, <b>operators</b> approve,{" "}
          <b>admins</b> manage everything.
        </p>
        <p className="text-muted-foreground">Without an auth token configured, the dashboard stays fully open.</p>
      </>
    ),
  },
  {
    icon: <Hourglass className="h-8 w-8 text-primary" />,
    title: "Approvals need a human",
    body: (
      <>
        <p>
          Calls judged <b>approval_required</b> wait here. Approve or deny them; the count badge on the tab
          tracks what&apos;s pending.
        </p>
        <p className="text-muted-foreground">
          Approvals live in memory and vanish on gateway restart. The list auto-refreshes every 10 seconds.
        </p>
      </>
    ),
  },
  {
    icon: <ScrollText className="h-8 w-8 text-primary" />,
    title: "Audit shows everything",
    body: (
      <>
        <p>
          Every decision is logged with the <b>exact command or file path</b> in the Detail column, plus
          risk, policy rule, and latency.
        </p>
        <p className="text-muted-foreground">Filter by decision and page through history. Stats up top summarize the mix.</p>
      </>
    ),
  },
  {
    icon: <Rocket className="h-8 w-8 text-primary" />,
    title: "Generate some traffic",
    body: (
      <>
        <p>
          Nothing here yet? Run a check from your terminal and watch it land in Audit:
        </p>
        <p className="rounded-md border bg-muted/50 px-3 py-2 font-mono text-xs">
          jev-guard check --tool terminal --command &quot;git status&quot;
        </p>
        <p className="text-muted-foreground">
          Or connect an agent via the OpenCode/Kiro plugins. You&apos;re ready.
        </p>
      </>
    ),
  },
]

export function wasOnboarded(): boolean {
  try {
    return localStorage.getItem(FLAG) === "1"
  } catch {
    return true // storage unavailable: don't nag every load
  }
}

export function markOnboarded() {
  try {
    localStorage.setItem(FLAG, "1")
  } catch {
    /* ignore */
  }
}

interface OnboardingTourProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

export function OnboardingTour({ open, onOpenChange }: OnboardingTourProps) {
  const [step, setStep] = useState(0)

  useEffect(() => {
    if (open) setStep(0)
  }, [open ])

  const last = step === STEPS.length - 1
  const s = STEPS[step]

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-3">
            {s.icon}
            {s.title}
          </DialogTitle>
          <DialogDescription asChild>
            <div className="space-y-2 pt-2 text-left">{s.body}</div>
          </DialogDescription>
        </DialogHeader>
        <div className="flex items-center gap-1.5" aria-label={`step ${step + 1} of ${STEPS.length}`}>
          {STEPS.map((_, i) => (
            <button
              key={i}
              aria-label={`go to step ${i + 1}`}
              onClick={() => setStep(i)}
              className={`h-1.5 flex-1 rounded-full ${i === step ? "bg-primary" : "bg-muted"}`}
            />
          ))}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>
            {last ? "Close" : "Skip"}
          </Button>
          {!last && (
            <Button variant="outline" onClick={() => setStep((v) => Math.max(0, v - 1))} disabled={step === 0}>
              Back
            </Button>
          )}
          {!last ? (
            <Button onClick={() => setStep((v) => v + 1)}>Next</Button>
          ) : (
            <Button onClick={() => onOpenChange(false)}>Get started</Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
