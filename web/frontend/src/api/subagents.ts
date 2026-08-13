import { launcherFetch } from "@/api/http"

export type Dict = Record<string, unknown>

export interface SubagentsData {
  // id -> entry fields (without the id key)
  agents: Record<string, Dict>
  // full existing agents.list (preserved on save)
  rawList: Dict[]
}

export const SUBAGENT_IDS = ["atomizer", "reflexor", "mcp_guard", "archivist"]

function asDict(v: unknown): Dict {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as Dict)
    : {}
}

function asStr(v: unknown): string {
  return typeof v === "string" ? v : ""
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { errors?: string[]; error?: string }
      if (Array.isArray(body.errors) && body.errors.length > 0) {
        message = body.errors.join("; ")
      } else if (typeof body.error === "string" && body.error.trim() !== "") {
        message = body.error
      }
    } catch {
      // Keep fallback error message when response body is not JSON.
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export async function getSubagents(): Promise<SubagentsData> {
  const cfg = asDict(await request<Dict>("/api/config"))
  const agentsCfg = asDict(cfg.agents)
  const list = Array.isArray(agentsCfg.list) ? (agentsCfg.list as Dict[]) : []

  const agents: Record<string, Dict> = {}
  for (const id of SUBAGENT_IDS) {
    agents[id] = {}
  }
  for (const raw of list) {
    const entry = asDict(raw)
    const id = asStr(entry.id)
    if (SUBAGENT_IDS.includes(id)) {
      const rest = { ...entry }
      delete rest.id
      agents[id] = rest
    }
  }
  return { agents, rawList: list }
}

// saveSubagents upserts the 4 subagent entries by id and sends the FULL
// merged list — merge-patch replaces arrays wholesale, so we must not send
// a partial list.
export async function saveSubagents(data: SubagentsData): Promise<void> {
  const merged: Dict[] = []
  const seen = new Set<string>()
  for (const raw of data.rawList) {
    const entry = asDict(raw)
    const id = asStr(entry.id)
    if (SUBAGENT_IDS.includes(id)) {
      merged.push({ id, ...data.agents[id] })
      seen.add(id)
    } else {
      merged.push(raw)
    }
  }
  for (const id of SUBAGENT_IDS) {
    if (!seen.has(id) && Object.keys(data.agents[id]).length > 0) {
      merged.push({ id, ...data.agents[id] })
    }
  }
  await request<Dict>("/api/config", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ agents: { list: merged } }),
  })
}

// getPath/setPath address nested keys via dot paths ("memory_brief.soft_limit").
export function getPath(obj: Dict, path: string): unknown {
  let cur: unknown = obj
  for (const part of path.split(".")) {
    cur = asDict(cur)[part]
  }
  return cur
}

export function setPath(obj: Dict, path: string, value: unknown): void {
  const parts = path.split(".")
  let cur = obj
  for (let i = 0; i < parts.length - 1; i++) {
    const next = asDict(cur[parts[i]])
    cur[parts[i]] = next
    cur = next
  }
  const last = parts[parts.length - 1]
  if (value === undefined || value === "") {
    delete cur[last]
  } else {
    cur[last] = value
  }
}
