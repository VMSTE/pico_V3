import { launcherFetch } from "@/api/http"

export interface PikaPeriodStats {
  requests: number
  tokens: number
  errors: number
  error_pct: number
}

export interface PikaComponentStats {
  component: string
  requests: number
  tokens: number
  errors: number
  avg_ms: number
}

export interface PikaOverview {
  available: boolean
  db_path?: string
  note?: string
  today: PikaPeriodStats
  totals: PikaPeriodStats
  p95_ms: number
  components: PikaComponentStats[]
}

export interface PikaRequestRow {
  ts: string
  component: string
  model: string
  task_tag: string
  prompt_tokens: number
  completion_tokens: number
  response_ms: number
  error: string
  tool_calls_requested: number
  tool_calls_success: number
  tool_calls_failed: number
}

export interface PikaRequestsResponse {
  available: boolean
  requests: PikaRequestRow[]
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const body = (await res.json()) as { message?: string; error?: string }
      if (typeof body.message === "string" && body.message.trim() !== "") {
        message = body.message
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

export async function getPikaOverview(): Promise<PikaOverview> {
  return request<PikaOverview>("/api/pika/overview")
}

export async function getPikaRequests(
  limit = 50,
): Promise<PikaRequestsResponse> {
  return request<PikaRequestsResponse>(`/api/pika/requests?limit=${limit}`)
}
