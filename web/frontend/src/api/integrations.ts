// Волна 114 срез 2 (ТЗ-114): GitHub OAuth-интеграция — статус и отключение.
// Подключение — обычная ссылка на /api/integrations/github/connect (редирект на GitHub).

export interface GitHubIntegrationStatus {
  connected: boolean
  login?: string
  expires_at?: string
}

export async function getGitHubIntegrationStatus(): Promise<GitHubIntegrationStatus> {
  const res = await fetch("/api/integrations/github/status")
  if (!res.ok) throw new Error("status " + res.status)
  return res.json()
}

export async function disconnectGitHubIntegration(): Promise<void> {
  const res = await fetch("/api/integrations/github/disconnect", { method: "POST" })
  if (!res.ok) throw new Error("status " + res.status)
}
