import { useCallback, useEffect, useRef, useState } from "react"
import { useTranslation } from "react-i18next"

import {
  type SessionSummary,
  deleteSession,
  getSessions,
  renameSession,
} from "@/api/sessions"

const LIMIT = 20

interface UseSessionHistoryOptions {
  activeSessionId: string
  onDeletedActiveSession: () => void
}

export function useSessionHistory({
  activeSessionId,
  onDeletedActiveSession,
}: UseSessionHistoryOptions) {
  const { t } = useTranslation()
  const observerRef = useRef<HTMLDivElement>(null)
  const [sessions, setSessions] = useState<SessionSummary[]>([])
  const [offset, setOffset] = useState(0)
  const [hasMore, setHasMore] = useState(true)
  const [isLoadingMore, setIsLoadingMore] = useState(false)
  const [loadError, setLoadError] = useState(false)
  const [searchQuery, setSearchQuery] = useState("")

  const loadSessions = useCallback(
    async (reset = true) => {
      try {
        const currentOffset = reset ? 0 : offset
        if (reset) {
          setLoadError(false)
          setHasMore(true)
          setOffset(0)
        }

        const data = await getSessions(currentOffset, LIMIT, searchQuery)
        setLoadError(false)

        if (data.length < LIMIT) {
          setHasMore(false)
        }

        if (reset) {
          setSessions(data)
        } else {
          setSessions((prev) => {
            const existingIds = new Set(prev.map((s) => s.id))
            const newItems = data.filter((s) => !existingIds.has(s.id))
            return [...prev, ...newItems]
          })
        }

        setOffset(currentOffset + data.length)
      } catch (err) {
        console.error("Failed to fetch session history:", err)
        setLoadError(true)
        if (!reset) {
          setHasMore(false)
        }
      } finally {
        setIsLoadingMore(false)
      }
    },
    [offset, searchQuery],
  )

  // Reload from scratch when the search query changes (skip first render —
  // the menu loads sessions on open).
  const isFirstSearchRender = useRef(true)
  useEffect(() => {
    if (isFirstSearchRender.current) {
      isFirstSearchRender.current = false
      return
    }
    void loadSessions(true)
  }, [searchQuery, loadSessions])

  useEffect(() => {
    if (!observerRef.current || !hasMore || isLoadingMore || loadError) return

    const observer = new IntersectionObserver(
      (entries) => {
        if (
          entries[0].isIntersecting &&
          hasMore &&
          !isLoadingMore &&
          !loadError
        ) {
          setIsLoadingMore(true)
          void loadSessions(false)
        }
      },
      { threshold: 0.1 },
    )

    observer.observe(observerRef.current)
    return () => observer.disconnect()
  }, [hasMore, isLoadingMore, loadError, loadSessions])

  const handleDeleteSession = useCallback(
    async (id: string) => {
      try {
        const deletedLoadedSession = sessions.some(
          (session) => session.id === id,
        )
        await deleteSession(id)
        setSessions((prev) => prev.filter((s) => s.id !== id))
        if (deletedLoadedSession) {
          setOffset((prev) => Math.max(prev - 1, 0))
        }
        if (id === activeSessionId) {
          onDeletedActiveSession()
        }
      } catch (err) {
        console.error("Failed to delete session:", err)
      }
    },
    [activeSessionId, onDeletedActiveSession, sessions],
  )

  // D-AUDIT-109: bulk hide — founder's requirement (select many, remove at
  // once). Hide-only: messages stay in bot_memory.db.
  const handleDeleteSessions = useCallback(
    async (ids: string[]) => {
      const results = await Promise.allSettled(
        ids.map((id) => deleteSession(id)),
      )
      const removed = new Set(
        ids.filter((_, index) => results[index].status === "fulfilled"),
      )
      setSessions((prev) => prev.filter((s) => !removed.has(s.id)))
      setOffset((prev) => Math.max(prev - removed.size, 0))
      if (removed.has(activeSessionId)) {
        onDeletedActiveSession()
      }
    },
    [activeSessionId, onDeletedActiveSession],
  )

  const handleRenameSession = useCallback(
    async (id: string, title: string) => {
      try {
        await renameSession(id, title)
        setSessions((prev) =>
          prev.map((s) => (s.id === id ? { ...s, title } : s)),
        )
      } catch (err) {
        console.error("Failed to rename session:", err)
      }
    },
    [],
  )

  return {
    sessions,
    hasMore,
    loadError,
    loadErrorMessage: t("chat.historyLoadFailed"),
    observerRef,
    searchQuery,
    setSearchQuery,
    loadSessions,
    handleDeleteSession,
    handleDeleteSessions,
    handleRenameSession,
  }
}
