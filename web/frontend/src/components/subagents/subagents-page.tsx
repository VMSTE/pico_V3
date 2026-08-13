import { IconLoader2, IconRobot } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getPath,
  getSubagents,
  saveSubagents,
  setPath,
  SUBAGENT_IDS,
  type Dict,
} from "@/api/subagents"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const inputClass =
  "border-border bg-background text-foreground placeholder:text-muted-foreground w-full rounded-md border px-3 py-2 text-sm outline-none focus:border-primary"

interface FieldDef {
  path: string
  kind: "bool" | "number" | "text"
  step?: string
  placeholder?: string
}

const COMMON_FIELDS: FieldDef[] = [
  { path: "enabled", kind: "bool" },
  { path: "model", kind: "text", placeholder: "background" },
  { path: "prompt_file", kind: "text" },
]

const AGENT_FIELDS: Record<string, FieldDef[]> = {
  atomizer: [
    { path: "trigger_tokens", kind: "number" },
    { path: "chunk_max_tokens", kind: "number" },
  ],
  reflexor: [
    { path: "timeout_ms", kind: "number" },
    { path: "schedule.daily", kind: "text", placeholder: "03:00" },
    { path: "schedule.weekly", kind: "text", placeholder: "Sun 04:00" },
    { path: "schedule.monthly", kind: "text", placeholder: "1st 05:00" },
  ],
  mcp_guard: [
    { path: "timeout_ms", kind: "number" },
    { path: "suspicious_text_ratio", kind: "number", step: "0.05" },
    { path: "suspicious_size_multiplier", kind: "number", step: "0.1" },
    { path: "startup_audit_enabled", kind: "bool" },
    { path: "reaudit_on_list_changed", kind: "bool" },
    { path: "hash_algorithm", kind: "text", placeholder: "sha256" },
  ],
  archivist: [
    { path: "max_tool_calls", kind: "number" },
    { path: "build_prompt_timeout_ms", kind: "number" },
    { path: "memory_brief.soft_limit", kind: "number" },
    { path: "memory_brief.hard_limit", kind: "number" },
    { path: "memory_brief.max_retries", kind: "number" },
    { path: "reasoning_guided_retrieval", kind: "bool" },
    { path: "reasoning_drift_overlap_min", kind: "number", step: "0.05" },
  ],
}

export function SubagentsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [data, setData] = useState<Record<string, Dict> | null>(null)
  const [rawList, setRawList] = useState<Dict[]>([])

  const query = useQuery({ queryKey: ["subagents"], queryFn: getSubagents })

  useEffect(() => {
    if (query.data) {
      setData(structuredClone(query.data.agents))
      setRawList(query.data.rawList)
    }
  }, [query.data])

  const setField = (agent: string, path: string, value: unknown) => {
    setData((prev) => {
      if (!prev) return prev
      const next = structuredClone(prev)
      setPath(next[agent], path, value)
      return next
    })
  }

  const saveMutation = useMutation({
    mutationFn: () => saveSubagents({ agents: data ?? {}, rawList }),
    onSuccess: () => {
      toast.success(t("pages.subagents.save_success"))
      void queryClient.invalidateQueries({ queryKey: ["subagents"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.subagents.save_error"),
      )
    },
  })

  const renderField = (agentId: string, def: FieldDef) => {
    const value = data ? getPath(data[agentId], def.path) : undefined
    if (def.kind === "bool") {
      return (
        <label key={def.path} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={value === true}
            onChange={(e) => setField(agentId, def.path, e.target.checked)}
          />
          <span className="font-mono text-xs">{def.path}</span>
        </label>
      )
    }
    return (
      <label key={def.path} className="flex flex-col gap-1 text-sm">
        <span className="font-mono text-xs">{def.path}</span>
        <input
          type={def.kind === "number" ? "number" : "text"}
          step={def.step}
          className={inputClass}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={def.placeholder}
          onChange={(e) => {
            const raw = e.target.value
            if (def.kind === "number") {
              setField(agentId, def.path, raw === "" ? undefined : Number(raw))
            } else {
              setField(agentId, def.path, raw)
            }
          }}
        />
      </label>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.subagents")}
        children={
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || query.isLoading || !data}
          >
            {saveMutation.isPending ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconRobot className="size-4" />
            )}
            {t("common.save")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.subagents.description")}
        </p>
        <p className="text-muted-foreground text-xs">
          {t("pages.subagents.note_defaults")}
        </p>

        {query.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : query.isError ? (
          <p className="text-destructive text-sm">
            {t("pages.subagents.load_error")}
          </p>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            {SUBAGENT_IDS.map((agentId) => (
              <section
                key={agentId}
                className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4"
              >
                <h2 className="text-sm font-semibold">
                  {t(`pages.subagents.${agentId}.name`)}
                </h2>
                <p className="text-muted-foreground text-xs">
                  {t(`pages.subagents.${agentId}.desc`)}
                </p>
                <div className="grid gap-3 sm:grid-cols-2">
                  {[...COMMON_FIELDS, ...AGENT_FIELDS[agentId]].map((def) =>
                    renderField(agentId, def),
                  )}
                </div>
              </section>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
