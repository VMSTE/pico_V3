// Волна 106 (ТЗ-106, срез C): страница /logs 2.0.
// Селектор уровня = ФИЛЬТР ВИДА (не пишет конфиг, мгновенно, ничего не теряет).
// Источник: gateway/errors/launcher. Поиск с подсветкой, follow/pause,
// экспорт и копирование текущего вида. Брендинг — АтоМинд (см. index.html).

import {
  IconCopy,
  IconDownload,
  IconPlayerPause,
  IconPlayerPlay,
  IconSearch,
} from "@tabler/icons-react"
import { useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"

import { type LogEntry, type LogSource } from "@/api/logs"
import { PageHeader } from "@/components/page-header"
import { Button } from "@/components/ui/button"
import { useFileLogs } from "@/hooks/use-file-logs"

const LEVELS = ["all", "debug", "info", "warn", "error"] as const

const LEVEL_COLOR: Record<string, string> = {
  debug: "text-muted-foreground",
  info: "text-blue-500",
  warn: "text-amber-500",
  warning: "text-amber-500",
  error: "text-red-500",
  fatal: "text-red-600",
  panic: "text-red-600",
}

function formatTime(t?: string): string {
  if (!t) return ""
  const d = new Date(t)
  if (Number.isNaN(d.getTime())) return t
  return d.toLocaleTimeString()
}

function highlight(text: string, q: string) {
  if (!q) return text
  const idx = text.toLowerCase().indexOf(q.toLowerCase())
  if (idx < 0) return text
  return (
    <>
      {text.slice(0, idx)}
      <mark className="rounded-sm bg-yellow-300/60 text-inherit">
        {text.slice(idx, idx + q.length)}
      </mark>
      {text.slice(idx + q.length)}
    </>
  )
}

function LogRow({ entry, q }: { entry: LogEntry; q: string }) {
  const color = LEVEL_COLOR[entry.level] ?? "text-muted-foreground"
  return (
    <div
      className="hover:bg-muted/40 flex gap-2 px-4 py-0.5 font-mono text-xs sm:px-8"
      title={entry.caller}
    >
      <span className="text-muted-foreground w-20 shrink-0">
        {formatTime(entry.time)}
      </span>
      <span className={`w-12 shrink-0 uppercase ${color}`}>{entry.level}</span>
      {entry.component ? (
        <span className="text-muted-foreground shrink-0">
          {"[" + entry.component + "]"}
        </span>
      ) : null}
      <span className="break-all whitespace-pre-wrap">
        {highlight(entry.message, q)}
      </span>
    </div>
  )
}

export function LogsPage() {
  const { t } = useTranslation()
  const [source, setSource] = useState<LogSource>("gateway")
  const [level, setLevel] = useState<string>("all")
  const [searchInput, setSearchInput] = useState("")
  const [q, setQ] = useState("")
  const [follow, setFollow] = useState(true)
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickRef = useRef(true)

  // Дебаунс поиска — не дёргаем API на каждый символ.
  useEffect(() => {
    const tm = setTimeout(() => setQ(searchInput.trim()), 300)
    return () => clearTimeout(tm)
  }, [searchInput])

  const { entries, available, path, total, matched } = useFileLogs(
    source,
    level,
    q,
    follow,
  )

  // Автоскролл вниз, только если пользователь у нижнего края.
  useEffect(() => {
    if (follow && stickRef.current && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [entries, follow])

  const exportLog = () => {
    const text = entries.map((e) => e.raw).join("\n") + "\n"
    const url = URL.createObjectURL(new Blob([text], { type: "text/plain" }))
    const a = document.createElement("a")
    a.href = url
    a.download = `${source}.log`
    a.click()
    URL.revokeObjectURL(url)
  }

  const copyLog = async () => {
    try {
      await navigator.clipboard.writeText(
        entries.map((e) => e.raw).join("\n"),
      )
      toast.success(t("logsPage.copied"))
    } catch {
      toast.error("clipboard")
    }
  }

  return (
    <div className="flex h-full flex-col">
      <PageHeader title={t("navigation.logs")}>
        <Button
          variant={follow ? "default" : "outline"}
          size="sm"
          onClick={() => setFollow((v) => !v)}
        >
          {follow ? (
            <IconPlayerPause className="size-4" />
          ) : (
            <IconPlayerPlay className="size-4" />
          )}
          {follow ? t("logsPage.pause") : t("logsPage.follow")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={copyLog}
          disabled={entries.length === 0}
        >
          <IconCopy className="size-4" />
          {t("logsPage.copy")}
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={exportLog}
          disabled={entries.length === 0}
        >
          <IconDownload className="size-4" />
          {t("logsPage.export")}
        </Button>
      </PageHeader>

      <div className="flex flex-wrap items-center gap-2 border-b px-4 py-2 sm:px-8">
        <div className="flex gap-1">
          {(["gateway", "errors", "launcher"] as LogSource[]).map((s) => (
            <Button
              key={s}
              variant={source === s ? "secondary" : "ghost"}
              size="sm"
              onClick={() => setSource(s)}
            >
              {t(`logsPage.source_${s}`)}
            </Button>
          ))}
        </div>
        <div className="flex gap-1">
          {LEVELS.map((lv) => (
            <button
              key={lv}
              type="button"
              onClick={() => setLevel(lv)}
              className={`rounded-md px-2 py-1 font-mono text-xs uppercase transition-colors ${
                level === lv
                  ? "bg-primary text-primary-foreground"
                  : "text-muted-foreground hover:bg-muted"
              }`}
            >
              {lv === "all" ? t("logsPage.level_all") : lv}
            </button>
          ))}
        </div>
        <div className="relative ml-auto w-56">
          <IconSearch className="text-muted-foreground absolute left-2 top-1/2 size-4 -translate-y-1/2" />
          <input
            value={searchInput}
            onChange={(e) => setSearchInput(e.target.value)}
            placeholder={t("logsPage.search_placeholder")}
            className="border-border bg-background placeholder:text-muted-foreground focus:border-primary w-full rounded-md border py-1 pl-8 pr-2 text-sm outline-none"
          />
        </div>
      </div>

      <div className="text-muted-foreground border-b px-4 py-1 font-mono text-xs sm:px-8">
        {t("logsPage.counts", { matched, total })}
        {path ? ` · ${path}` : ""}
      </div>

      <div
        ref={scrollRef}
        className="flex-1 overflow-y-auto py-2"
        onScroll={(e) => {
          const el = e.currentTarget
          stickRef.current =
            el.scrollHeight - el.scrollTop - el.clientHeight < 80
        }}
      >
        {!available ? (
          <p className="text-muted-foreground p-4 text-sm sm:px-8">
            {t("logsPage.unavailable")}
          </p>
        ) : entries.length === 0 ? (
          <p className="text-muted-foreground p-4 text-sm sm:px-8">
            {t("logsPage.empty")}
          </p>
        ) : (
          entries.map((e) => <LogRow key={e.n} entry={e} q={q} />)
        )}
      </div>
    </div>
  )
}
