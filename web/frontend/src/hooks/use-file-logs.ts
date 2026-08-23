// Волна 106 (ТЗ-106, срез C): хук файловых логов — первая порция с хвоста,
// дальше follow по offset раз в секунду; reset при ротации файла.
// Работает и при остановленном gateway: читается файл, а не буфер.

import { useEffect, useRef, useState } from "react"

import { getLogEntries, type LogEntry, type LogSource } from "@/api/logs"

const POLL_MS = 1000
const VIEW_LIMIT = 300
const MAX_VIEW = 2000

export function useFileLogs(
  source: LogSource,
  level: string,
  q: string,
  follow: boolean,
) {
  const [entries, setEntries] = useState<LogEntry[]>([])
  const [available, setAvailable] = useState(true)
  const [path, setPath] = useState("")
  const [total, setTotal] = useState(0)
  const [matched, setMatched] = useState(0)
  const offsetRef = useRef(0)
  const syncRef = useRef(0)

  // Полная перезагрузка при смене источника/уровня/поиска.
  useEffect(() => {
    const token = ++syncRef.current
    offsetRef.current = 0
    setEntries([])
    let cancelled = false
    getLogEntries({ source, level, q, limit: VIEW_LIMIT })
      .then((data) => {
        if (cancelled || token !== syncRef.current) return
        setAvailable(data.available)
        setPath(data.path ?? "")
        setTotal(data.total ?? 0)
        setMatched(data.matched ?? 0)
        setEntries(data.entries)
        const last = data.entries[data.entries.length - 1]
        offsetRef.current = last ? last.n : 0
      })
      .catch(() => {})
    return () => {
      cancelled = true
    }
  }, [source, level, q])

  // Follow: дочитываем новые строки раз в секунду.
  useEffect(() => {
    if (!follow) return
    let mounted = true
    let timer: ReturnType<typeof setTimeout>
    const tick = async () => {
      try {
        const token = syncRef.current
        const data = await getLogEntries({
          source,
          level,
          q,
          offset: offsetRef.current,
          limit: VIEW_LIMIT,
        })
        if (!mounted || token !== syncRef.current) return
        setTotal(data.total ?? 0)
        setMatched(data.matched ?? 0)
        setPath(data.path ?? "")
        if (data.reset) {
          setEntries(data.entries)
        } else if (data.entries.length > 0) {
          setEntries((prev) => [...prev, ...data.entries].slice(-MAX_VIEW))
        }
        const last = data.entries[data.entries.length - 1]
        if (last) offsetRef.current = last.n
      } catch {
        // Транзиентные ошибки поллинга молча переживаем.
      } finally {
        if (mounted) timer = setTimeout(tick, POLL_MS)
      }
    }
    timer = setTimeout(tick, POLL_MS)
    return () => {
      mounted = false
      clearTimeout(timer)
    }
  }, [follow, source, level, q])

  return { entries, available, path, total, matched }
}
