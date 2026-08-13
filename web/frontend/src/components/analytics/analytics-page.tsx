import { IconChartBar, IconLoader2 } from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { getAnalyticsSlice, saveAnalyticsSlice } from "@/api/analytics"
import { getPath, setPath, type Dict } from "@/api/subagents"
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

interface SectionDef {
  key: string
  fields: FieldDef[]
}

const SECTIONS: SectionDef[] = [
  {
    key: "general",
    fields: [
      { path: "enabled", kind: "bool" },
      { path: "queries_dir", kind: "text", placeholder: "/workspace/queries" },
      { path: "disable_telegram_reports", kind: "bool" },
      { path: "schedule.weekly", kind: "text", placeholder: "Sun 06:00" },
      { path: "schedule.monthly", kind: "text", placeholder: "1st 07:00" },
    ],
  },
  {
    key: "thresholds",
    fields: [
      { path: "tool_fail_rate_pct", kind: "number", step: "0.5", placeholder: "10" },
      { path: "error_rate_pct", kind: "number", step: "0.5", placeholder: "5" },
      { path: "latency_p95_ms", kind: "number", placeholder: "15000" },
      { path: "unused_atoms_pct", kind: "number", step: "1", placeholder: "20" },
      { path: "stale_atoms_pct", kind: "number", step: "1", placeholder: "10" },
      { path: "subagent_errors", kind: "number", placeholder: "5" },
      { path: "delta_significant_pct", kind: "number", step: "5", placeholder: "50" },
    ],
  },
  {
    key: "reports",
    fields: [
      { path: "report_max_telegram_chars", kind: "number", placeholder: "4000" },
      { path: "top_tools_limit", kind: "number", placeholder: "10" },
      { path: "top_atoms_limit", kind: "number", placeholder: "10" },
      { path: "top_tasks_limit", kind: "number", placeholder: "5" },
    ],
  },
]

export function AnalyticsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [data, setData] = useState<Dict | null>(null)

  const query = useQuery({
    queryKey: ["analytics-slice"],
    queryFn: getAnalyticsSlice,
  })

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
    mutationFn: () => saveAnalyticsSlice(data ?? {}),
    onSuccess: () => {
      toast.success(t("pages.analytics.save_success"))
      void queryClient.invalidateQueries({ queryKey: ["analytics-slice"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.analytics.save_error"),
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
          step={def.step}
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
        title={t("navigation.analytics")}
        children={
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || query.isLoading || !data}
          >
            {saveMutation.isPending ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconChartBar className="size-4" />
            )}
            {t("common.save")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-4 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.analytics.description")}
        </p>
        <p className="text-muted-foreground text-xs">
          {t("pages.analytics.note_defaults")}
        </p>

        {query.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : query.isError ? (
          <p className="text-destructive text-sm">
            {t("pages.analytics.load_error")}
          </p>
        ) : (
          <div className="grid gap-4 xl:grid-cols-2">
            {SECTIONS.map((section) => (
              <section
                key={section.key}
                className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4"
              >
                <h2 className="text-sm font-semibold">
                  {t(`pages.analytics.${section.key}.title`)}
                </h2>
                <p className="text-muted-foreground text-xs">
                  {t(`pages.analytics.${section.key}.desc`)}
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
