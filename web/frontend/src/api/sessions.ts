import { launcherFetch } from "@/api/http"

export interface SessionSummary {
  id: string
  title: string
  preview: string
  message_count: number
  created: string
  updated: string
  /** D-AUDIT-109: true when the chat can be resumed (pico mapping known). */
  resumable: boolean
}

export interface SessionDetail {
  id: string
  messages: {
    role: "user" | "assistant"
    content: string
    kind?: "normal" | "thought" | "tool_calls"
    media?: string[]
    attachments?: {
      type?: "image" | "audio" | "video" | "file"
      url: string
      filename?: string
      content_type?: string
    }[]
    tool_calls?: {
      id?: string
      type?: string
      function?: {
        name?: string
        arguments?: string
      }
      extra_content?: {
        tool_feedback_explanation?: string
      }
    }[]
  }[]
  summary: string
  created: string
  updated: string
}

export async function getSessions(
  offset: number = 0,
  limit: number = 20,
  q: string = "",
): Promise<SessionSummary[]> {
  const params = new URLSearchParams({
    offset: offset.toString(),
    limit: limit.toString(),
  })
  if (q.trim()) {
    params.set("q", q.trim())
  }

  const res = await launcherFetch(`/api/sessions?${params.toString()}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch sessions: ${res.status}`)
  }
  return res.json()
}

export async function getSessionHistory(id: string): Promise<SessionDetail> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`)
  if (!res.ok) {
    throw new Error(`Failed to fetch session ${id}: ${res.status}`)
  }
  return res.json()
}

export async function renameSession(id: string, title: string): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "PATCH",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ title }),
  })
  if (!res.ok) {
    throw new Error(`Failed to rename session: ${res.status}`)
  }
}

export async function deleteSession(id: string): Promise<void> {
  const res = await launcherFetch(`/api/sessions/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
  if (!res.ok) {
    throw new Error(`Failed to delete session: ${res.status}`)
  }
}
