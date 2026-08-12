import { launcherFetch } from "@/api/http"

export interface MCPServerInfo {
  name: string
  type: string
  target: string
  enabled: boolean
  deferred?: boolean
  env_keys?: string[]
  env_file?: string
  headers?: string[]
}

export interface MCPServersResponse {
  enabled: boolean
  servers: MCPServerInfo[]
}

export interface MCPServerPayload {
  enabled: boolean
  deferred?: boolean
  command?: string
  args?: string[]
  env?: Record<string, string>
  env_file?: string
  type?: string
  url?: string
  headers?: Record<string, string>
}

export interface MCPTestResponse {
  status: string
  tool_count?: number
  message?: string
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

export async function getMCPServers(): Promise<MCPServersResponse> {
  return request<MCPServersResponse>("/api/mcp/servers")
}

export async function putMCPServer(
  name: string,
  payload: MCPServerPayload,
): Promise<{ status: string }> {
  return request<{ status: string }>(
    `/api/mcp/servers/${encodeURIComponent(name)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    },
  )
}

export async function deleteMCPServer(
  name: string,
): Promise<{ status: string }> {
  return request<{ status: string }>(
    `/api/mcp/servers/${encodeURIComponent(name)}`,
    { method: "DELETE" },
  )
}

export async function testMCPServer(name: string): Promise<MCPTestResponse> {
  return request<MCPTestResponse>(
    `/api/mcp/servers/${encodeURIComponent(name)}/test`,
    { method: "POST" },
  )
}
