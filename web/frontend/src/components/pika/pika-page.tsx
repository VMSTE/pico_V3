import { IconActivity, IconLoader2, IconRefresh } from "@tabler/icons-react"
import { useQuery } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"

import { getPikaOverview, getPikaRequests } from "@/api/pika"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

function formatNumber(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M"
  if (n >= 10_000) return (n / 1_000).toFixed(1) + "K"
  return n.toLocaleString()
}

function formatMs(ms: number): string {
  if (ms <= 0) return "—"
  if (ms >= 1000) return (ms / 1000).toFixed(1) + " s"
  return ms + " ms"
}

const thClass = "text-muted-foreground px-3 py-2 text-left text-xs font-medium"
const tdClass = "px-3 py-2 text-sm"

export function PikaPage() {
  const { t } = useTranslation()

  const overviewQuery = useQuery({
    queryKey: ["pika-overview"],
    queryFn: getPikaOverview,
    refetchInterval: 30000,
  })
  const requestsQuery = useQuery({
    queryKey: ["pika-requests"],
    queryFn: () => getPikaRequests(50),
    refetchInterval: 30000,
  })

  const refresh = () => {
    void overviewQuery.refetch()
    void requestsQuery.refetch()
  }

  const ov = overviewQuery.data
  const requests = requestsQuery.data?.requests ?? []

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.pika")}
        children={
          <Button
            variant="outline"
            size="sm"
            onClick={refresh}
            disabled={overviewQuery.isFetching || requestsQuery.isFetching}
          >
            {overviewQuery.isFetching ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconRefresh className="size-4" />
            )}
            {t("pages.pika.refresh")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-6 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.pika.description")}
        </p>

        {overviewQuery.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : overviewQuery.isError || !ov ? (
          <p className="text-destructive text-sm">{t("pages.pika.load_error")}</p>
        ) : !ov.available ? (
          <div className="border-border/40 bg-muted/5 flex flex-col items-center justify-center gap-3 rounded-xl border border-dashed py-16 text-center shadow-sm">
            <div className="bg-muted mb-2 rounded-full p-4">
              <IconActivity className="text-muted-foreground size-6" />
            </div>
            <h3 className="text-lg font-semibold tracking-tight">
              {t("pages.pika.unavailable")}
            </h3>
          </div>
        ) : (
          <>
            <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
              <StatCard
                label={t("pages.pika.today")}
                value={formatNumber(ov.today.tokens)}
                sub={
                  ov.today.requests +
                  " " +
                  t("pages.pika.requests").toLowerCase()
                }
              />
              <StatCard
                label={t("pages.pika.totals")}
                value={formatNumber(ov.totals.tokens)}
                sub={
                  ov.totals.requests +
                  " " +
                  t("pages.pika.requests").toLowerCase()
                }
              />
              <StatCard
                label={t("pages.pika.error_rate")}
                value={ov.today.error_pct.toFixed(1) + "%"}
                sub={
                  ov.today.errors +
                  " / " +
                  ov.today.requests +
                  " " +
                  t("pages.pika.errors").toLowerCase()
                }
              />
              <StatCard
                label={t("pages.pika.p95")}
                value={formatMs(ov.p95_ms)}
                sub={t("pages.pika.totals").toLowerCase()}
              />
            </div>

            <div>
              <h2 className="mb-2 text-sm font-semibold">
                {t("pages.pika.components_title")}
              </h2>
              <div className="border-border/60 overflow-x-auto rounded-xl border">
                <table className="w-full">
                  <thead className="bg-muted/40">
                    <tr>
                      <th className={thClass}>{t("pages.pika.component")}</th>
                      <th className={thClass}>{t("pages.pika.requests")}</th>
                      <th className={thClass}>{t("pages.pika.tokens")}</th>
                      <th className={thClass}>{t("pages.pika.errors")}</th>
                      <th className={thClass}>{t("pages.pika.avg_ms")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(ov.components ?? []).map((c) => (
                      <tr
                        key={c.component}
                        className="border-border/40 border-t"
                      >
                        <td className={tdClass + " font-mono"}>{c.component}</td>
                        <td className={tdClass}>{formatNumber(c.requests)}</td>
                        <td className={tdClass}>{formatNumber(c.tokens)}</td>
                        <td
                          className={
                            tdClass +
                            (c.errors > 0 ? " text-destructive" : "")
                          }
                        >
                          {c.errors}
                        </td>
                        <td className={tdClass}>{formatMs(c.avg_ms)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>

            <div>
              <h2 className="mb-2 text-sm font-semibold">
                {t("pages.pika.recent_title")}
              </h2>
              <div className="border-border/60 overflow-x-auto rounded-xl border">
                <table className="w-full">
                  <thead className="bg-muted/40">
                    <tr>
                      <th className={thClass}>{t("pages.pika.col_time")}</th>
                      <th className={thClass}>{t("pages.pika.component")}</th>
                      <th className={thClass}>{t("pages.pika.col_model")}</th>
                      <th className={thClass}>{t("pages.pika.col_task")}</th>
                      <th className={thClass}>{t("pages.pika.col_tokens")}</th>
                      <th className={thClass}>{t("pages.pika.col_ms")}</th>
                      <th className={thClass}>{t("pages.pika.col_error")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {requests.map((r, i) => (
                      <tr
                        key={r.ts + "-" + i}
                        className="border-border/40 border-t"
                      >
                        <td className={tdClass + " text-muted-foreground text-xs"}>
                          {r.ts}
                        </td>
                        <td className={tdClass + " font-mono text-xs"}>
                          {r.component}
                        </td>
                        <td className={tdClass + " text-xs"}>{r.model}</td>
                        <td className={tdClass + " text-xs"}>
                          {r.task_tag || "—"}
                        </td>
                        <td className={tdClass}>
                          {formatNumber(r.prompt_tokens + r.completion_tokens)}
                        </td>
                        <td className={tdClass}>{formatMs(r.response_ms)}</td>
                        <td
                          className={
                            tdClass +
                            " text-xs" +
                            (r.error ? " text-destructive" : "")
                          }
                        >
                          {r.error || "—"}
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  )
}

function StatCard({
  label,
  value,
  sub,
}: {
  label: string
  value: string
  sub: string
}) {
  return (
    <div className="border-border/60 bg-muted/10 rounded-xl border p-4">
      <div className="text-muted-foreground text-xs">{label}</div>
      <div className="mt-1 text-2xl font-semibold">{value}</div>
      <div className="text-muted-foreground mt-1 text-xs">{sub}</div>
    </div>
  )
}
