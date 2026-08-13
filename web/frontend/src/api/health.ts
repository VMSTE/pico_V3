import { launcherFetch } from "@/api/http"
import type { Dict } from "@/api/subagents"

function asDict(v: unknown): Dict {
  return v !== null && typeof v === "object" && !Array.isArray(v)
    ? (v as Dict)
    : {}
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

// getHealthSlice returns the raw health section of the config.
export async function getHealthSlice(): Promise<Dict> {
  const cfg = asDict(await request<Dict>("/api/config"))
  return asDict(cfg.health)
}

export async function saveHealthSlice(health: Dict): Promise<void> {
  await request<Dict>("/api/config", {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ health }),
  })
}
