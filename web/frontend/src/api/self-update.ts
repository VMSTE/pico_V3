export interface CommandInfo {
  name: string
  description: string
  usage?: string
}

export async function fetchCommands(): Promise<CommandInfo[]> {
  const res = await fetch("/api/commands")
  if (!res.ok) throw new Error(`commands: HTTP ${res.status}`)
  const data = await res.json()
  return data.commands ?? []
}

export interface SourceUpdateResult {
  status: string
  message?: string
  log?: string
  launcher_restart_required?: boolean
  /** D-AUDIT-110: launcher is re-execing itself; page reload will follow. */
  relaunching?: boolean
}

export async function updateFromSource(): Promise<SourceUpdateResult> {
  const res = await fetch("/api/update-from-source", { method: "POST" })
  try {
    return await res.json()
  } catch {
    return { status: "error", message: `HTTP ${res.status}` }
  }
}
