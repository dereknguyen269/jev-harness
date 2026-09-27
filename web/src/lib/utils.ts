import { clsx, type ClassValue } from "clsx"
import { twMerge } from "tailwind-merge"

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs))
}

const ENTITIES: Record<string, string> = {
  "&": "&amp;",
  "<": "&lt;",
  ">": "&gt;",
  '"': "&quot;",
}

// Escape for the confirm-dialog lines (rendered via dangerouslySetInnerHTML).
export function escHtml(s: string): string {
  return String(s ?? "").replace(/[&<>"]/g, (c) => ENTITIES[c])
}

// Role model (mirrors the backend): viewer reads; operator also approves
// and reloads; admin also manages users/rules/groups.
export const isAdminRole = (role: string) => role === "admin"
export const canOperateRole = (role: string) => role === "admin" || role === "operator"
