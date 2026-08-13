import { launcherFetch } from "@/api/http"

export interface OpRow {
  key: string
  level: string
  confirm: string
}

export interface SecuritySlice {
  confirmTimeoutMin: number
  criticalPaths: string[]
  ops: OpRow[]
  radEnabled: boolean
  radDrift: number
  radBlock: number
  radWarn: number
  managerChannel: string
  managerChatId: string
}

type Dict = Record<string, unknown>

function asDict(v: unknown): Dict {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as Dict)
    : {}
}

function asStr(v: unknown): string {
  return typeof v === "string" ? v : ""
}

function asNum(v: unknown, fallback: number): number {
  return typeof v === "number" && Number.isFinite(v) ? v : fallback
}

function asStrList(v: unknown): string[] {
  return Array.isArray(v) ? v.filter((x) => typeof x === "string") : []
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

export async function getSecuritySlice(): Promise<SecuritySlice> {
  const cfg = asDict(await request<Dict>("/api/config"))
  const dops = asDict(asDict(cfg.security).dangerous_ops)
  const rad = asDict(asDict(cfg.security).rad)
  const reporting = asDict(asDict(cfg.health).reporting)

  const ops: OpRow[] = Object.entries(asDict(dops.ops)).map(([key, v]) => {
    const entry = asDict(v)
    return {
      key,
      level: asStr(entry.level) || "medium",
      confirm: asStr(entry.confirm) || "always",
    }
  })
  ops.sort((a, b) => a.key.localeCompare(b.key))

  return {
    confirmTimeoutMin: asNum(dops.confirm_timeout_min, 30),
    criticalPaths: asStrList(dops.critical_paths),
    ops,
    radEnabled: rad.enabled === true,
    radDrift: asNum(rad.drift_threshold, 0.2),
    radBlock: asNum(rad.block_score, 3),
    radWarn: asNum(rad.warn_score, 2),
    managerChannel: asStr(reporting.manager_channel),
    managerChatId: asStr(reporting.manager_chat_id),
  }
}

// saveSecuritySlice patches the config. Removed op keys are sent as null —
// the backend merge semantics delete them.
export async function saveSecuritySlice(
  slice: SecuritySlice,
  removedOpKeys: string[],
): Promise<void> {
  const ops: Record<string, unknown> = {}
  for (const k of removedOpKeys) {
    ops[k] = null
  }
  for (const row of slice.ops) {
    ops[row.key] = { level: row.level, confirm: row.confirm }
  }
  await request<Dict>("/api/config", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      security: {
        dangerous_ops: {
          confirm_timeout_min: slice.confirmTimeoutMin,
          critical_paths: slice.criticalPaths,
          ops,
        },
        rad: {
          enabled: slice.radEnabled,
          drift_threshold: slice.radDrift,
          block_score: slice.radBlock,
          warn_score: slice.radWarn,
        },
      },
      health: {
        reporting: {
          manager_channel: slice.managerChannel,
          manager_chat_id: slice.managerChatId,
        },
      },
    }),
  })
}
