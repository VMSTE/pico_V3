import {
  IconActivity,
  IconLoader2,
  IconPencil,
  IconPlug,
  IconPlus,
  IconTrash,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  deleteMCPServer,
  getMCPServers,
  patchMCPServer,
  putMCPServer,
  testMCPServer,
  type MCPServerInfo,
  type MCPServerPayload,
} from "@/api/mcp"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const inputClass =
  "border-border bg-background text-foreground placeholder:text-muted-foreground w-full rounded-md border px-3 py-2 text-sm outline-none focus:border-primary"

type Transport = "stdio" | "http" | "sse"

function parseKeyValueLines(
  text: string,
  separator: "equals" | "colon",
): Record<string, string> | undefined {
  const out: Record<string, string> = {}
  for (const rawLine of text.split("\n")) {
    const line = rawLine.trim()
    if (!line) continue
    const idx = separator === "equals" ? line.indexOf("=") : line.indexOf(":")
    if (idx <= 0) continue
    out[line.slice(0, idx).trim()] = line.slice(idx + 1).trim()
  }
  return Object.keys(out).length > 0 ? out : undefined
}

function mapToLines(map: Record<string, string> | undefined, sep: string): string {
  return Object.entries(map ?? {})
    .map(([k, v]) => k + sep + v)
    .join("\n")
}

export function MCPPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [showForm, setShowForm] = useState(false)
  const [editingName, setEditingName] = useState<string | null>(null)
  const [editEnabled, setEditEnabled] = useState(true)
  const [name, setName] = useState("")
  const [transport, setTransport] = useState<Transport>("stdio")
  const [command, setCommand] = useState("")
  const [argsText, setArgsText] = useState("")
  const [url, setUrl] = useState("")
  const [envText, setEnvText] = useState("")
  const [headersText, setHeadersText] = useState("")
  const [deferred, setDeferred] = useState(false)

  const serversQuery = useQuery({
    queryKey: ["mcp-servers"],
    queryFn: getMCPServers,
  })

  const resetForm = () => {
    setEditingName(null)
    setEditEnabled(true)
    setName("")
    setTransport("stdio")
    setCommand("")
    setArgsText("")
    setUrl("")
    setEnvText("")
    setHeadersText("")
    setDeferred(false)
  }

  const startEdit = (srv: MCPServerInfo) => {
    setEditingName(srv.name)
    setEditEnabled(srv.enabled)
    setName(srv.name)
    const tr: Transport =
      srv.type === "stdio" || srv.type === "http" || srv.type === "sse"
        ? srv.type
        : srv.command
          ? "stdio"
          : "http"
    setTransport(tr)
    setCommand(srv.command ?? "")
    setArgsText((srv.args ?? []).join(" "))
    setUrl(srv.url ?? "")
    setEnvText(mapToLines(srv.env, "="))
    setHeadersText(mapToLines(srv.headers, ": "))
    setDeferred(srv.deferred === true)
    setShowForm(true)
  }

  const saveMutation = useMutation({
    mutationFn: async () => {
      const trimmedName = name.trim()
      if (!/^[A-Za-z0-9_-]+$/.test(trimmedName)) {
        throw new Error(t("pages.mcp.form.validation_target"))
      }
      const payload: MCPServerPayload = {
        enabled: editingName !== null ? editEnabled : true,
        type: transport,
        deferred: deferred ? true : undefined,
      }
      if (transport === "stdio") {
        if (command.trim() === "") {
          throw new Error(t("pages.mcp.form.validation_target"))
        }
        payload.command = command.trim()
        const args = argsText.split(/\s+/).filter(Boolean)
        if (args.length > 0) payload.args = args
        const env = parseKeyValueLines(envText, "equals")
        if (env) payload.env = env
      } else {
        if (url.trim() === "") {
          throw new Error(t("pages.mcp.form.validation_target"))
        }
        payload.url = url.trim()
        const headers = parseKeyValueLines(headersText, "colon")
        if (headers) payload.headers = headers
      }
      return putMCPServer(trimmedName, payload)
    },
    onSuccess: () => {
      toast.success(t("pages.mcp.save_success"))
      void queryClient.invalidateQueries({ queryKey: ["mcp-servers"] })
      resetForm()
      setShowForm(false)
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("pages.mcp.save_error"))
    },
  })

  const toggleMutation = useMutation({
    mutationFn: (srv: MCPServerInfo) =>
      patchMCPServer(srv.name, { enabled: !srv.enabled }),
    onSuccess: () => {
      toast.success(t("pages.mcp.patch_success"))
      void queryClient.invalidateQueries({ queryKey: ["mcp-servers"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.mcp.toggle_error"),
      )
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (serverName: string) => deleteMCPServer(serverName),
    onSuccess: () => {
      toast.success(t("pages.mcp.delete_success"))
      void queryClient.invalidateQueries({ queryKey: ["mcp-servers"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.mcp.delete_error"),
      )
    },
  })

  const testMutation = useMutation({
    mutationFn: (serverName: string) => testMCPServer(serverName),
    onSuccess: (data) => {
      toast.success(
        t("pages.mcp.test_success", { count: data.tool_count ?? 0 }),
      )
    },
    onError: (err) => {
      toast.error(err instanceof Error ? err.message : t("pages.mcp.test_failed"))
    },
  })

  const servers = serversQuery.data?.servers ?? []

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.mcp")}
        children={
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              resetForm()
              setShowForm((v) => !v)
            }}
          >
            <IconPlus className="size-4" />
            {t("pages.mcp.add_server")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.mcp.description")}
        </p>

        {serversQuery.data && !serversQuery.data.enabled && (
          <div className="border-yellow-500/40 bg-yellow-500/10 rounded-md border px-3 py-2 text-sm">
            {t("pages.mcp.global_disabled")}
          </div>
        )}

        {showForm && (
          <div className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4">
            <div className="grid gap-3 sm:grid-cols-2">
              <label className="flex flex-col gap-1 text-sm">
                <span>{t("pages.mcp.form.name")}</span>
                <input
                  className={inputClass}
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder={t("pages.mcp.form.name_placeholder")}
                  disabled={editingName !== null}
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                <span>{t("pages.mcp.form.type")}</span>
                <select
                  className={inputClass}
                  value={transport}
                  onChange={(e) => setTransport(e.target.value as Transport)}
                >
                  <option value="stdio">stdio</option>
                  <option value="http">http</option>
                  <option value="sse">sse</option>
                </select>
              </label>
            </div>

            {transport === "stdio" ? (
              <>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.mcp.form.command")}</span>
                  <input
                    className={inputClass}
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                    placeholder={t("pages.mcp.form.command_placeholder")}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.mcp.form.args")}</span>
                  <input
                    className={inputClass}
                    value={argsText}
                    onChange={(e) => setArgsText(e.target.value)}
                    placeholder={t("pages.mcp.form.args_placeholder")}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.mcp.form.env")}</span>
                  <textarea
                    className={inputClass}
                    rows={3}
                    value={envText}
                    onChange={(e) => setEnvText(e.target.value)}
                    placeholder={t("pages.mcp.form.env_placeholder")}
                  />
                </label>
              </>
            ) : (
              <>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.mcp.form.url")}</span>
                  <input
                    className={inputClass}
                    value={url}
                    onChange={(e) => setUrl(e.target.value)}
                    placeholder={t("pages.mcp.form.url_placeholder")}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.mcp.form.headers")}</span>
                  <textarea
                    className={inputClass}
                    rows={3}
                    value={headersText}
                    onChange={(e) => setHeadersText(e.target.value)}
                    placeholder={t("pages.mcp.form.headers_placeholder")}
                  />
                </label>
              </>
            )}

            <label className="flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                checked={deferred}
                onChange={(e) => setDeferred(e.target.checked)}
              />
              {t("pages.mcp.form.deferred_label")}
            </label>

            {editingName !== null && (
              <p className="text-muted-foreground text-xs">
                {t("pages.mcp.secret_keep_hint")}
              </p>
            )}

            <div className="flex gap-2">
              <Button
                size="sm"
                onClick={() => saveMutation.mutate()}
                disabled={saveMutation.isPending}
              >
                {saveMutation.isPending ? (
                  <IconLoader2 className="size-4 animate-spin" />
                ) : null}
                {t("common.save")}
              </Button>
              <Button
                variant="outline"
                size="sm"
                onClick={() => {
                  resetForm()
                  setShowForm(false)
                }}
              >
                {t("common.cancel")}
              </Button>
            </div>
          </div>
        )}

        {serversQuery.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : serversQuery.isError ? (
          <p className="text-destructive text-sm">{t("pages.mcp.load_error")}</p>
        ) : servers.length === 0 ? (
          <div className="border-border/40 bg-muted/5 flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed py-16 text-center shadow-sm">
            <div className="bg-muted mb-2 rounded-full p-4">
              <IconPlug className="text-muted-foreground size-6" />
            </div>
            <h3 className="text-lg font-semibold tracking-tight">
              {t("pages.mcp.empty")}
            </h3>
          </div>
        ) : (
          <div className="grid gap-4 lg:grid-cols-2">
            {servers.map((srv) => (
              <div
                key={srv.name}
                className="border-border/60 bg-muted/10 flex flex-col gap-2 rounded-xl border p-4"
              >
                <div className="flex items-center justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-sm font-semibold">
                      {srv.name}
                    </span>
                    <span className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 text-xs">
                      {srv.type}
                    </span>
                    <button
                      type="button"
                      onClick={() => toggleMutation.mutate(srv)}
                      disabled={toggleMutation.isPending}
                      className={
                        srv.enabled
                          ? "cursor-pointer rounded bg-green-500/15 px-1.5 py-0.5 text-xs text-green-600 dark:text-green-400"
                          : "bg-muted text-muted-foreground cursor-pointer rounded px-1.5 py-0.5 text-xs"
                      }
                    >
                      {srv.enabled
                        ? t("pages.mcp.enabled")
                        : t("pages.mcp.disabled")}
                    </button>
                  </div>
                  <div className="flex gap-1">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => startEdit(srv)}
                    >
                      <IconPencil className="size-4" />
                      {t("pages.mcp.edit")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={testMutation.isPending}
                      onClick={() => testMutation.mutate(srv.name)}
                    >
                      {testMutation.isPending &&
                      testMutation.variables === srv.name ? (
                        <IconLoader2 className="size-4 animate-spin" />
                      ) : (
                        <IconActivity className="size-4" />
                      )}
                      {t("pages.mcp.test")}
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      disabled={deleteMutation.isPending}
                      onClick={() => {
                        if (
                          window.confirm(
                            t("pages.mcp.confirm_delete", { name: srv.name }),
                          )
                        ) {
                          deleteMutation.mutate(srv.name)
                        }
                      }}
                    >
                      <IconTrash className="size-4" />
                      {t("pages.mcp.delete")}
                    </Button>
                  </div>
                </div>
                <div className="text-muted-foreground font-mono text-xs break-all">
                  {srv.target}
                </div>
                {(Object.keys(srv.env ?? {}).length > 0 ||
                  Object.keys(srv.headers ?? {}).length > 0) && (
                  <div className="flex flex-wrap gap-1">
                    {Object.keys(srv.env ?? {}).map((k) => (
                      <span
                        key={`env-${k}`}
                        className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs"
                      >
                        {k}
                      </span>
                    ))}
                    {Object.keys(srv.headers ?? {}).map((k) => (
                      <span
                        key={`hdr-${k}`}
                        className="bg-muted text-muted-foreground rounded px-1.5 py-0.5 font-mono text-xs"
                      >
                        {k}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
