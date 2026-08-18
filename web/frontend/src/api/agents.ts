import { launcherFetch } from "@/api/http"

export interface AgentInfo {
  id: string
  name: string
  default: boolean
  workspace: string
  model: string
  skills?: string[]
  allow_agents?: string[]
  description?: string
  has_agent_md: boolean
}

export interface AgentsData {
  agents: AgentInfo[]
  defaults_workspace: string
}

export interface AgentPayload {
  name: string
  description: string
  workspace: string
  model: string
  skills: string[]
  allow_agents: string[]
  default: boolean
}

async function request<T>(path: string, options?: RequestInit): Promise<T> {
  const res = await launcherFetch(path, options)
  if (!res.ok) {
    let message = `API error: ${res.status} ${res.statusText}`
    try {
      const text = await res.text()
      if (text.trim() !== "") {
        message = text.trim()
      }
    } catch {
      // Keep fallback error message when the body cannot be read.
    }
    throw new Error(message)
  }
  return res.json() as Promise<T>
}

export async function getAgents(): Promise<AgentsData> {
  return request<AgentsData>("/api/agents")
}

export async function createAgent(
  id: string,
  payload: AgentPayload,
): Promise<void> {
  await request<unknown>("/api/agents", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id, ...payload }),
  })
}

export async function updateAgent(
  id: string,
  payload: AgentPayload,
): Promise<void> {
  await request<unknown>(`/api/agents/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })
}

export async function deleteAgent(id: string): Promise<void> {
  await request<unknown>(`/api/agents/${encodeURIComponent(id)}`, {
    method: "DELETE",
  })
}
