import { IconLoader2, IconPlus, IconRefresh, IconTrash } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  createAgent,
  deleteAgent,
  getAgents,
  updateAgent,
  type AgentInfo,
  type AgentPayload,
} from "@/api/agents"
import { restartGateway } from "@/api/gateway"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const inputClass =
  "border-border bg-background text-foreground placeholder:text-muted-foreground w-full rounded-md border px-3 py-2 text-sm outline-none focus:border-primary"

interface AgentFormState {
  name: string
  description: string
  workspace: string
  model: string
  skillsText: string
  allowText: string
  isDefault: boolean
}

const emptyForm: AgentFormState = {
  name: "",
  description: "",
  workspace: "",
  model: "",
  skillsText: "",
  allowText: "",
  isDefault: false,
}

function splitList(text: string): string[] {
  return text
    .split(",")
    .map((s) => s.trim())
    .filter((s) => s !== "")
}

function toPayload(form: AgentFormState): AgentPayload {
  return {
    name: form.name.trim(),
    description: form.description.trim(),
    workspace: form.workspace.trim(),
    model: form.model.trim(),
    skills: splitList(form.skillsText),
    allow_agents: splitList(form.allowText),
    default: form.isDefault,
  }
}

function fromAgent(agent: AgentInfo): AgentFormState {
  return {
    name: agent.name,
    description: agent.description ?? "",
    workspace: agent.workspace,
    model: agent.model,
    skillsText: (agent.skills ?? []).join(", "),
    allowText: (agent.allow_agents ?? []).join(", "),
    isDefault: agent.default,
  }
}

function AgentForm({
  form,
  setForm,
}: {
  form: AgentFormState
  setForm: (next: AgentFormState) => void
}) {
  const { t } = useTranslation()
  const set = (patch: Partial<AgentFormState>) => setForm({ ...form, ...patch })
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-mono text-xs">name</span>
        <input
          className={inputClass}
          value={form.name}
          onChange={(e) => set({ name: e.target.value })}
        />
      </label>
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-mono text-xs">model</span>
        <input
          className={inputClass}
          placeholder={t("pages.agents.model_hint", "model_list name, empty = default")}
          value={form.model}
          onChange={(e) => set({ model: e.target.value })}
        />
      </label>
      <label className="flex flex-col gap-1 text-sm sm:col-span-2">
        <span className="font-mono text-xs">workspace</span>
        <input
          className={inputClass}
          value={form.workspace}
          onChange={(e) => set({ workspace: e.target.value })}
        />
      </label>
      <label className="flex flex-col gap-1 text-sm sm:col-span-2">
        <span className="font-mono text-xs">description</span>
        <input
          className={inputClass}
          placeholder={t("pages.agents.desc_hint", "Shown to peers in agent discovery")}
          value={form.description}
          onChange={(e) => set({ description: e.target.value })}
        />
      </label>
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-mono text-xs">skills (comma separated)</span>
        <input
          className={inputClass}
          value={form.skillsText}
          onChange={(e) => set({ skillsText: e.target.value })}
        />
      </label>
      <label className="flex flex-col gap-1 text-sm">
        <span className="font-mono text-xs">allow_agents (comma separated)</span>
        <input
          className={inputClass}
          placeholder="researcher, coder"
          value={form.allowText}
          onChange={(e) => set({ allowText: e.target.value })}
        />
      </label>
      <label className="flex items-center gap-2 text-sm">
        <input
          type="checkbox"
          checked={form.isDefault}
          onChange={(e) => set({ isDefault: e.target.checked })}
        />
        <span className="font-mono text-xs">default agent</span>
      </label>
    </div>
  )
}

export function AgentsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [showCreate, setShowCreate] = useState(false)
  const [createId, setCreateId] = useState("")
  const [createForm, setCreateForm] = useState<AgentFormState>(emptyForm)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [editForm, setEditForm] = useState<AgentFormState>(emptyForm)
  const [needsRestart, setNeedsRestart] = useState(false)

  const query = useQuery({ queryKey: ["agents"], queryFn: getAgents })

  const onError = (err: unknown) =>
    toast.error(err instanceof Error ? err.message : String(err))

  const createMutation = useMutation({
    mutationFn: () => createAgent(createId.trim(), toPayload(createForm)),
    onSuccess: () => {
      toast.success(t("pages.agents.created", "Agent created"))
      setShowCreate(false)
      setCreateId("")
      setCreateForm(emptyForm)
      setNeedsRestart(true)
      void queryClient.invalidateQueries({ queryKey: ["agents"] })
    },
    onError,
  })

  const updateMutation = useMutation({
    mutationFn: (id: string) => updateAgent(id, toPayload(editForm)),
    onSuccess: () => {
      toast.success(t("pages.agents.saved", "Agent saved"))
      setEditingId(null)
      setNeedsRestart(true)
      void queryClient.invalidateQueries({ queryKey: ["agents"] })
    },
    onError,
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => deleteAgent(id),
    onSuccess: () => {
      toast.success(t("pages.agents.deleted", "Agent deleted"))
      setNeedsRestart(true)
      void queryClient.invalidateQueries({ queryKey: ["agents"] })
    },
    onError,
  })

  const restartMutation = useMutation({
    mutationFn: restartGateway,
    onSuccess: () => {
      setNeedsRestart(false)
      toast.success(t("pages.agents.restarted", "Gateway restarted"))
    },
    onError,
  })

  const defaultsWorkspace = query.data?.defaults_workspace ?? ""

  const renderCard = (agent: AgentInfo) => {
    const isEditing = editingId === agent.id
    return (
      <section
        key={agent.id}
        className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4"
      >
        <div className="flex items-center justify-between gap-2">
          <h2 className="text-sm font-semibold">
            {agent.name || agent.id}
            <span className="text-muted-foreground ml-2 font-mono text-xs">
              {agent.id}
            </span>
          </h2>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                if (isEditing) {
                  setEditingId(null)
                } else {
                  setEditForm(fromAgent(agent))
                  setEditingId(agent.id)
                }
              }}
            >
              {isEditing
                ? t("common.cancel", "Cancel")
                : t("common.edit", "Edit")}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={agent.default || deleteMutation.isPending}
              onClick={() => {
                if (
                  window.confirm(
                    t(
                      "pages.agents.confirm_delete",
                      "Delete agent? Workspace files are preserved.",
                    ),
                  )
                ) {
                  deleteMutation.mutate(agent.id)
                }
              }}
            >
              <IconTrash className="size-4" />
            </Button>
          </div>
        </div>
        {isEditing ? (
          <>
            <AgentForm form={editForm} setForm={setEditForm} />
            <div>
              <Button
                size="sm"
                onClick={() => updateMutation.mutate(agent.id)}
                disabled={updateMutation.isPending}
              >
                {updateMutation.isPending ? (
                  <IconLoader2 className="size-4 animate-spin" />
                ) : null}
                {t("common.save", "Save")}
              </Button>
            </div>
          </>
        ) : (
          <div className="text-muted-foreground grid gap-1 text-xs">
            {agent.description ? <p>{agent.description}</p> : null}
            <p>model: {agent.model || "—"}</p>
            <p className="font-mono">{agent.workspace}</p>
            <p>
              {agent.default ? "default · " : ""}
              {agent.has_agent_md ? "AGENT.md ✓" : "AGENT.md ✗"}
              {agent.allow_agents && agent.allow_agents.length > 0
                ? ` · can spawn: ${agent.allow_agents.join(", ")}`
                : ""}
            </p>
          </div>
        )}
      </section>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.agents", "Agents")}
        children={
          <Button
            size="sm"
            onClick={() => {
              setShowCreate(!showCreate)
              if (!showCreate && defaultsWorkspace !== "") {
                setCreateForm((prev) => ({
                  ...prev,
                  workspace:
                    prev.workspace === ""
                      ? `${defaultsWorkspace}-new-agent`
                      : prev.workspace,
                }))
              }
            }}
          >
            <IconPlus className="size-4" />
            {t("pages.agents.new", "New agent")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t(
            "pages.agents.description",
            "Named agents with their own workspace, model and prompt (AGENT.md). " +
              "Allow a peer in allow_agents and the model can delegate tasks to it.",
          )}
        </p>

        {needsRestart ? (
          <div className="border-border/60 bg-muted/20 flex items-center justify-between gap-3 rounded-xl border p-3">
            <p className="text-sm">
              {t(
                "pages.agents.restart_needed",
                "Agent list changes apply after a gateway restart.",
              )}
            </p>
            <Button
              size="sm"
              variant="outline"
              onClick={() => restartMutation.mutate()}
              disabled={restartMutation.isPending}
            >
              {restartMutation.isPending ? (
                <IconLoader2 className="size-4 animate-spin" />
              ) : (
                <IconRefresh className="size-4" />
              )}
              {t("pages.agents.restart", "Restart gateway")}
            </Button>
          </div>
        ) : null}

        {showCreate ? (
          <section className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4">
            <h2 className="text-sm font-semibold">
              {t("pages.agents.create_title", "New agent")}
            </h2>
            <label className="flex flex-col gap-1 text-sm">
              <span className="font-mono text-xs">id</span>
              <input
                className={inputClass}
                placeholder="researcher"
                value={createId}
                onChange={(e) => {
                  const id = e.target.value
                  setCreateId(id)
                  if (defaultsWorkspace !== "") {
                    setCreateForm((prev) => ({
                      ...prev,
                      workspace: `${defaultsWorkspace}-${id || "new-agent"}`,
                    }))
                  }
                }}
              />
            </label>
            <AgentForm form={createForm} setForm={setCreateForm} />
            <div>
              <Button
                size="sm"
                onClick={() => createMutation.mutate()}
                disabled={createMutation.isPending || createId.trim() === ""}
              >
                {createMutation.isPending ? (
                  <IconLoader2 className="size-4 animate-spin" />
                ) : null}
                {t("common.create", "Create")}
              </Button>
            </div>
          </section>
        ) : null}

        {query.isLoading ? (
          <p className="text-muted-foreground text-sm">
            {t("labels.loading", "Loading…")}
          </p>
        ) : query.isError ? (
          <p className="text-destructive text-sm">
            {t("pages.agents.load_error", "Failed to load agents")}
          </p>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            {(query.data?.agents ?? []).map(renderCard)}
          </div>
        )}
      </div>
    </div>
  )
}
