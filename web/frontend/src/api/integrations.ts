// Волна 114 срез 2 (ТЗ-114): OAuth-интеграции — статус и отключение.
// Волна 117 (ТЗ-117): обобщение на провайдеров (github, notion, ...).
// Подключение — обычная ссылка на /api/integrations/<provider>/connect.

export interface IntegrationStatus {
  connected: boolean
  login?: string
  workspace?: string
  expires_at?: string
}

export async function getIntegrationStatus(
  provider: string,
): Promise<IntegrationStatus> {
  const res = await fetch(`/api/integrations/${provider}/status`)
  if (!res.ok) throw new Error("status " + res.status)
  return res.json()
}

export async function disconnectIntegration(provider: string): Promise<void> {
  const res = await fetch(`/api/integrations/${provider}/disconnect`, {
    method: "POST",
  })
  if (!res.ok) throw new Error("status " + res.status)
}
