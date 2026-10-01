import { useCallback, useEffect, useState } from "react"

export type Theme = "auto" | "light" | "dark"

function readStored(): Theme {
  try {
    const v = localStorage.getItem("jev-theme")
    if (v === "light" || v === "dark" || v === "auto") return v
  } catch {
    /* storage unavailable */
  }
  return "auto"
}

function systemDark() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches
}

export function useTheme() {
  const [theme, setThemeState] = useState<Theme>(readStored)
  const [osDark, setOsDark] = useState(systemDark)

  useEffect(() => {
    const mq = window.matchMedia("(prefers-color-scheme: dark)")
    const onChange = (e: MediaQueryListEvent) => setOsDark(e.matches)
    mq.addEventListener("change", onChange)
    return () => mq.removeEventListener("change", onChange)
  }, [])

  const resolved = theme === "auto" ? (osDark ? "dark" : "light") : theme

  useEffect(() => {
    document.documentElement.classList.toggle("dark", resolved === "dark")
    try {
      localStorage.setItem("jev-theme", theme)
    } catch {
      /* storage unavailable */
    }
  }, [theme, resolved])

  const cycle = useCallback(() => {
    setThemeState((t) => (t === "auto" ? "light" : t === "light" ? "dark" : "auto"))
  }, [])

  return { theme, resolved, cycle }
}
