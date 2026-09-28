export interface Rule {
  id: string
  tool: string
  pattern: string
  action: string
  priority: number
  group: string
  category: string
  business: string
  task: string
  description: string
  source: string
  approval_timeout: number
}

export type RuleInput = Omit<Rule, "source">

export interface User {
  id: string
  name: string
  email: string
  role: string
  api_key: string
  active: boolean
  created_at: string
}

export interface Approval {
  id: string
  request_id: string
  tool: string
  arguments?: Record<string, unknown>
  risk: number
  reason?: string
  status: "pending" | "approved" | "denied" | "expired"
  created_at: string
  expires_at: string
}

export interface AuditEvent {
  id: string
  timestamp: string
  agent: string
  tool: string
  command?: string
  path?: string
  resource?: string
  policy_decision?: string
  jev_decision?: string
  final_decision: string
  policy_id?: string
  reason_code?: string
  risk: number
  confidence: number
  latency_ms: number
}

export type Stats = Record<string, number>

const API = "/v1"
const TOKEN_KEY = "jev-auth"

export const getToken = (): string => {
  try {
    return localStorage.getItem(TOKEN_KEY) || ""
  } catch {
    return ""
  }
}

export const setToken = (t: string) => {
  try {
    localStorage.setItem(TOKEN_KEY, t)
  } catch {
    /* storage unavailable */
  }
}

export const clearToken = () => {
  try {
    localStorage.removeItem(TOKEN_KEY)
  } catch {
    /* storage unavailable */
  }
}

export const authStatus = () => api<{ auth: boolean }>("GET", "/auth/status")

export interface Identity {
  name: string
  role: string
}

const USER_KEY = "jev-user"

export const login = (secret: string) =>
  api<Identity>("POST", "/auth/login", { secret })

export const me = () => api<Identity>("GET", "/auth/me")

export const getUser = (): Identity | null => {
  try {
    const raw = localStorage.getItem(USER_KEY)
    return raw ? (JSON.parse(raw) as Identity) : null
  } catch {
    return null
  }
}

export const setUser = (u: Identity) => {
  try {
    localStorage.setItem(USER_KEY, JSON.stringify(u))
  } catch {
    /* storage unavailable */
  }
}

export const clearUser = () => {
  try {
    localStorage.removeItem(USER_KEY)
  } catch {
    /* storage unavailable */
  }
}

async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { "Content-Type": "application/json" }
  const token = getToken()
  if (token) headers["Authorization"] = `Bearer ${token}`
  const r = await fetch(API + path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
  if (r.status === 401) clearToken() // token rejected: force re-login
  if (r.status === 204) return null as T
  const t = await r.text()
  let j: unknown
  try {
    j = JSON.parse(t)
  } catch {
    j = t
  }
  if (!r.ok) {
    const msg =
      typeof j === "string" ? j : ((j as { error?: string })?.error || r.statusText || `HTTP ${r.status}`)
    // Carry the status so callers can tell 401 (bad token) from e.g. 503 (YAML-only mode).
    throw Object.assign(new Error(msg), { status: r.status })
  }
  return j as T
}

export const getHealth = () => api<{ status: string; version: string; jev: boolean }>("GET", "/health")

export const listRules = () => api<Rule[]>("GET", "/rules")
export const saveRule = (rule: RuleInput) =>
  rule.id
    ? api<Rule>("PUT", `/rules/${encodeURIComponent(rule.id)}`, rule)
    : api<Rule>("POST", "/rules", rule)
export const deleteRule = (id: string) => api<null>("DELETE", `/rules/${encodeURIComponent(id)}`)
export const reloadPolicy = () => api<{ status: string; rules: number }>("POST", "/policy/reload")
export const reseedPolicy = (mode: "merge" | "replace" = "merge", force = false) =>
  api<{ status: string; rules: number; groups: number; categories: number; pruned: number; live: number }>(
    "POST",
    "/policy/reseed",
    { mode, force },
  )

export const listUsers = () => api<User[]>("GET", "/users")
export const createUser = (u: { name: string; email: string; role: string }) =>
  api<User>("POST", "/users", u)
export const deleteUser = (id: string) => api<null>("DELETE", `/users/${encodeURIComponent(id)}`)

export const listApprovals = () => api<Approval[]>("GET", "/approvals")
export const decideApproval = (id: string, approve: boolean) =>
  api<Approval>("POST", `/approvals/${encodeURIComponent(id)}/${approve ? "approve" : "deny"}`)

export interface ApprovalsPage {
  approvals: Approval[]
  total: number
  page: number
  per_page: number
  pages: number
  pending: number
}

export const listApprovalsPage = (page: number, perPage: number) =>
  api<ApprovalsPage>("GET", `/approvals/page?page=${page}&per_page=${perPage}`)

export const getStats = () => api<Stats>("GET", "/stats")
export const listAudit = (decision?: string) =>
  api<AuditEvent[]>("GET", "/audit" + (decision ? `?decision=${encodeURIComponent(decision)}` : ""))

export interface JevCall {
  timestamp: string
  model: string
  endpoint: string
  status: "ok" | "error"
  http_status: number
  latency_ms: number
  input_tokens: number
  output_tokens: number
  error?: string
}

export const listJevCalls = (limit = 20) => api<JevCall[]>("GET", `/jev/calls?limit=${limit}`)

export interface Group {
  name: string
  description: string
}

export const listGroups = () => api<Group[]>("GET", "/groups")
export const saveGroup = (group: Group, isEdit: boolean) =>
  isEdit
    ? api<Group>("PUT", `/groups/${encodeURIComponent(group.name)}`, group)
    : api<Group>("POST", "/groups", group)
export const deleteGroup = (name: string) => api<null>("DELETE", `/groups/${encodeURIComponent(name)}`)

export interface Category {
  name: string
  description: string
}

export const listCategories = () => api<Category[]>("GET", "/categories")
export const saveCategory = (category: Category, isEdit: boolean) =>
  isEdit
    ? api<Category>("PUT", `/categories/${encodeURIComponent(category.name)}`, category)
    : api<Category>("POST", "/categories", category)
export const deleteCategory = (name: string) => api<null>("DELETE", `/categories/${encodeURIComponent(name)}`)

export interface Settings {
  approval_ttl_seconds: number
}

export const getSettings = () => api<Settings>("GET", "/settings")
export const saveSettings = (s: Settings) => api<Settings>("PUT", "/settings", s)
