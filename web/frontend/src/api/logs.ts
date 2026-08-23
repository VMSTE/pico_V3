// Волна 106 (ТЗ-106, срез C): клиент файловых логов /api/logs.

import { launcherFetch } from "@/api/http"

export type LogSource = "gateway" | "errors" | "launcher"

export interface LogEntry {
  n: number
  level: string
  time?: string
  component?: string
  caller?: string
  message: string
  fields?: Record<string, unknown>
  raw: string
}

export interface LogsResponse {
  available: boolean
  source: string
  path?: string
  total?: number
  matched?: number
  reset?: boolean
  entries: LogEntry[]
}

export async function getLogEntries(params: {
  source: LogSource
  level?: string
  q?: string
  offset?: number
  limit?: number
}): Promise<LogsResponse> {
  const sp = new URLSearchParams()
  sp.set("source", params.source)
  if (params.level && params.level !== "all") sp.set("level", params.level)
  if (params.q) sp.set("q", params.q)
  if (params.offset) sp.set("offset", String(params.offset))
  if (params.limit) sp.set("limit", String(params.limit))
  const res = await launcherFetch(`/api/logs?${sp.toString()}`)
  if (!res.ok) throw new Error(`logs: HTTP ${res.status}`)
  return (await res.json()) as LogsResponse
}
