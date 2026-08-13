import {
  IconLoader2,
  IconPlus,
  IconShieldLock,
  IconTrash,
} from "@tabler/icons-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import {
  getSecuritySlice,
  saveSecuritySlice,
  type OpRow,
} from "@/api/security"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"

const inputClass =
  "border-border bg-background text-foreground placeholder:text-muted-foreground w-full rounded-md border px-3 py-2 text-sm outline-none focus:border-primary"

const LEVELS = ["low", "medium", "high", "critical"]
const CONFIRMS = ["always", "never", "if_healthy", "if_critical_path"]

export function SecurityPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()

  const [confirmTimeoutMin, setConfirmTimeoutMin] = useState(30)
  const [criticalPathsText, setCriticalPathsText] = useState("")
  const [ops, setOps] = useState<OpRow[]>([])
  const [originalKeys, setOriginalKeys] = useState<string[]>([])
  const [radEnabled, setRadEnabled] = useState(true)
  const [radDrift, setRadDrift] = useState(0.2)
  const [radBlock, setRadBlock] = useState(3)
  const [radWarn, setRadWarn] = useState(2)
  const [managerChannel, setManagerChannel] = useState("")
  const [managerChatId, setManagerChatId] = useState("")

  const sliceQuery = useQuery({
    queryKey: ["security-slice"],
    queryFn: getSecuritySlice,
  })

  useEffect(() => {
    const s = sliceQuery.data
    if (!s) return
    setConfirmTimeoutMin(s.confirmTimeoutMin)
    setCriticalPathsText(s.criticalPaths.join("\n"))
    setOps(s.ops)
    setOriginalKeys(s.ops.map((o) => o.key))
    setRadEnabled(s.radEnabled)
    setRadDrift(s.radDrift)
    setRadBlock(s.radBlock)
    setRadWarn(s.radWarn)
    setManagerChannel(s.managerChannel)
    setManagerChatId(s.managerChatId)
  }, [sliceQuery.data])

  const saveMutation = useMutation({
    mutationFn: async () => {
      const cleanOps = ops.filter((o) => o.key.trim() !== "")
      const currentKeys = new Set(cleanOps.map((o) => o.key))
      const removed = originalKeys.filter((k) => !currentKeys.has(k))
      return saveSecuritySlice(
        {
          confirmTimeoutMin,
          criticalPaths: criticalPathsText
            .split("\n")
            .map((s) => s.trim())
            .filter(Boolean),
          ops: cleanOps,
          radEnabled,
          radDrift,
          radBlock,
          radWarn,
          managerChannel: managerChannel.trim(),
          managerChatId: managerChatId.trim(),
        },
        removed,
      )
    },
    onSuccess: () => {
      toast.success(t("pages.security.save_success"))
      void queryClient.invalidateQueries({ queryKey: ["security-slice"] })
    },
    onError: (err) => {
      toast.error(
        err instanceof Error ? err.message : t("pages.security.save_error"),
      )
    },
  })

  const updateOp = (idx: number, patch: Partial<OpRow>) => {
    setOps((prev) =>
      prev.map((o, i) => (i === idx ? { ...o, ...patch } : o)),
    )
  }

  const removeOp = (idx: number) => {
    const row = ops[idx]
    if (
      row.key.trim() !== "" &&
      !window.confirm(t("pages.security.delete_op_confirm", { name: row.key }))
    ) {
      return
    }
    setOps((prev) => prev.filter((_, i) => i !== idx))
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title={t("navigation.security")}
        children={
          <Button
            size="sm"
            onClick={() => saveMutation.mutate()}
            disabled={saveMutation.isPending || sliceQuery.isLoading}
          >
            {saveMutation.isPending ? (
              <IconLoader2 className="size-4 animate-spin" />
            ) : (
              <IconShieldLock className="size-4" />
            )}
            {t("common.save")}
          </Button>
        }
      />

      <div className="flex flex-1 flex-col gap-6 overflow-y-auto p-4 sm:p-8">
        <p className="text-muted-foreground text-sm">
          {t("pages.security.description")}
        </p>

        {sliceQuery.isLoading ? (
          <p className="text-muted-foreground text-sm">{t("labels.loading")}</p>
        ) : sliceQuery.isError ? (
          <p className="text-destructive text-sm">
            {t("pages.security.load_error")}
          </p>
        ) : (
          <>
            <section className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4">
              <h2 className="text-sm font-semibold">
                {t("pages.security.gate_title")}
              </h2>
              <p className="text-muted-foreground text-xs">
                {t("pages.security.gate_hint")}
              </p>
              <label className="flex max-w-xs flex-col gap-1 text-sm">
                <span>{t("pages.security.timeout")}</span>
                <input
                  type="number"
                  min={1}
                  className={inputClass}
                  value={confirmTimeoutMin}
                  onChange={(e) =>
                    setConfirmTimeoutMin(Number(e.target.value) || 30)
                  }
                />
              </label>
              <label className="flex flex-col gap-1 text-sm">
                <span>{t("pages.security.critical_paths")}</span>
                <textarea
                  className={inputClass}
                  rows={3}
                  value={criticalPathsText}
                  onChange={(e) => setCriticalPathsText(e.target.value)}
                  placeholder="**/workspace/prompts/**"
                />
                <span className="text-muted-foreground text-xs">
                  {t("pages.security.critical_paths_hint")}
                </span>
              </label>

              <h3 className="mt-2 text-xs font-semibold">
                {t("pages.security.ops_title")}
              </h3>
              <div className="flex flex-col gap-2">
                {ops.map((op, idx) => (
                  <div
                    key={idx}
                    className="grid grid-cols-[1fr_130px_170px_36px] items-center gap-2"
                  >
                    <input
                      className={inputClass + " font-mono text-xs"}
                      value={op.key}
                      onChange={(e) => updateOp(idx, { key: e.target.value })}
                      placeholder={t("pages.security.op_key")}
                    />
                    <select
                      className={inputClass}
                      value={op.level}
                      onChange={(e) => updateOp(idx, { level: e.target.value })}
                    >
                      {LEVELS.map((l) => (
                        <option key={l} value={l}>
                          {l}
                        </option>
                      ))}
                    </select>
                    <select
                      className={inputClass}
                      value={op.confirm}
                      onChange={(e) =>
                        updateOp(idx, { confirm: e.target.value })
                      }
                    >
                      {CONFIRMS.map((c) => (
                        <option key={c} value={c}>
                          {c}
                        </option>
                      ))}
                    </select>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => removeOp(idx)}
                    >
                      <IconTrash className="size-4" />
                    </Button>
                  </div>
                ))}
                <div>
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={() =>
                      setOps((prev) => [
                        ...prev,
                        { key: "", level: "medium", confirm: "always" },
                      ])
                    }
                  >
                    <IconPlus className="size-4" />
                    {t("pages.security.add_op")}
                  </Button>
                </div>
              </div>
            </section>

            <section className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4">
              <h2 className="text-sm font-semibold">
                {t("pages.security.rad_title")}
              </h2>
              <label className="flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={radEnabled}
                  onChange={(e) => setRadEnabled(e.target.checked)}
                />
                {t("pages.security.rad_enabled")}
              </label>
              <div className="grid gap-3 sm:grid-cols-3">
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.security.rad_drift")}</span>
                  <input
                    type="number"
                    step={0.1}
                    min={0}
                    max={1}
                    className={inputClass}
                    value={radDrift}
                    onChange={(e) => setRadDrift(Number(e.target.value) || 0)}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.security.rad_block")}</span>
                  <input
                    type="number"
                    min={1}
                    className={inputClass}
                    value={radBlock}
                    onChange={(e) => setRadBlock(Number(e.target.value) || 3)}
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.security.rad_warn")}</span>
                  <input
                    type="number"
                    min={1}
                    className={inputClass}
                    value={radWarn}
                    onChange={(e) => setRadWarn(Number(e.target.value) || 2)}
                  />
                </label>
              </div>
            </section>

            <section className="border-border/60 bg-muted/10 flex flex-col gap-3 rounded-xl border p-4">
              <h2 className="text-sm font-semibold">
                {t("pages.security.manager_title")}
              </h2>
              <p className="text-muted-foreground text-xs">
                {t("pages.security.manager_hint")}
              </p>
              <div className="grid gap-3 sm:grid-cols-2">
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.security.manager_channel")}</span>
                  <input
                    className={inputClass}
                    value={managerChannel}
                    onChange={(e) => setManagerChannel(e.target.value)}
                    placeholder="telegram"
                  />
                </label>
                <label className="flex flex-col gap-1 text-sm">
                  <span>{t("pages.security.manager_chat_id")}</span>
                  <input
                    className={inputClass}
                    value={managerChatId}
                    onChange={(e) => setManagerChatId(e.target.value)}
                    placeholder="123456789"
                  />
                </label>
              </div>
            </section>
          </>
        )}
      </div>
    </div>
  )
}
