import { IconHeartbeat, IconLoader2 } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { getHealthSlice, saveHealthSlice } from "@/api/health"
import { getPath, setPath, type Dict } from "@/api/subagents"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const inputClass =
  "border-border bg-background text-foreground placeholder:text-muted-foreground w-full rounded-md border px-3 py-2 text-sm outline-none focus:border-primary"

interface FieldDef {
  path: string
  kind: "bool" | "number" | "text"
  placeholder?: string
}

interface SectionDef {
  key: string
  fields: FieldDef[]
}

const SECTIONS: SectionDef[] = [
  {
    key: "thresholds",
    fields: [
      { path: "window_size", kind: "number" },
      { path: "tool_fail_threshold_pct", kind: "number" },
      { path: "latency_threshold_ms", kind: "number" },
    ],
  },
  {
    key: "fallback",
    fields: [
      { path: "fallback_provider.provider", kind: "text", placeholder: "stepfun" },
      { path: "fallback_provider.model", kind: "text", placeholder: "step-3.5-flash" },
      { path: "fallback_provider.api_key_env", kind: "text", placeholder: "STEPFUN_API_KEY" },
      { path: "fallback_provider.base_url", kind: "text" },
    ],
  },
  {
    key: "reporting",
    fields: [
      { path: "reporting.typing_indicator_enabled", kind: "bool" },
      { path: "reporting.alert_dedup_per_session", kind: "bool" },
      { path: "reporting.daily_health_summary_enabled", kind: "bool" },
    ],
  },
  {
    key: "progress",
    fields: [
      { path: "progress.enabled", kind: "bool" },
      { path: "progress.throttle_sec", kind: "number" },
      { path: "progress.delete_on_complete", kind: "bool" },
      { path: "progress.show_step_text", kind: "bool" },
      { path: "progress.stop_command_enabled", kind: "bool" },
    ],
  },
]

export function HealthPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [data, setData] = useState<Dict | null>(null)

  const query = useQuery({ queryKey: ["health-slice"], queryFn: getHealthSlice })

  useEffect(() => {
    if (query.data) {
      setData(structuredClone(query.data))
    }
  }, [query.data])

  const setField = (path: string, value: unknown) => {
    setData((prev) => {
      if (!prev) return prev
      const next = structuredClone(prev)
      setPath(next, path, value)
      return next
    })
  }

  const saveMutation = useMutation({
    mutationFn: () => saveHealthSlice(data ?? {}),
    onSuccess: () => {
      toast.success(t("pages.health.save_success"))
      void queryClient.invalidateQueries({ queryKey: ["health-slice"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.health.save_error"),
      )
    },
  })

  const renderField = (def: FieldDef) => {
    const value = data ? getPath(data, def.path) : undefined
    if (def.kind === "bool") {
      return (
        <label key={def.path} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={value === true}
            onChange={(e) => setField(def.path, e.target.checked)}
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
          className={inputClass}
          value={value === undefined || value === null ? "" : String(value)}
          placeholder={def.placeholder}
          onChange={(e) => {
            const raw = e.target.value
            if (def.kind === "number") {
              setField(def.path, raw === "" ? undefined : Number(raw))
            } else {
              setField(def.path, raw)
            }
          }}
        />
      </label>
    )
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.health")}
        children={
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || query.isLoading || !data}
          >
            {saveMutation.isPending ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconHeartbeat className="size-4" />
            )}
            {t("common.save")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.health.description")}
        </p>
        <p className="text-muted-foreground text-xs">
          {t("pages.health.note_defaults")}
        </p>

        {query.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : query.isError ? (
          <p className="text-destructive text-sm">
            {t("pages.health.load_error")}
          </p>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            {SECTIONS.map((section) => (
              <section
                key={section.key}
                className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4"
              >
                <h2 className="text-sm font-semibold">
                  {t(`pages.health.${section.key}.title`)}
                </h2>
                <p className="text-muted-foreground text-xs">
                  {t(`pages.health.${section.key}.desc`)}
                </p>
                <div className="grid gap-3 sm:grid-cols-2">
                  {section.fields.map(renderField)}
                </div>
              </section>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
