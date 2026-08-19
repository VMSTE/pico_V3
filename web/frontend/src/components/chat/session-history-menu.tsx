import {
  IconCheck,
  IconHistory,
  IconPencil,
  IconTrash,
  IconX,
} from "@tabler/icons-react"
import dayjs from "dayjs"
import { type RefObject, useState } from "react"
import { useTranslation } from "react-i18next"

import type { SessionSummary } from "@/api/sessions"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ScrollArea } from "@/components/ui/scroll-area"

interface SessionHistoryMenuProps {
  sessions: SessionSummary[]
  activeSessionId: string
  hasMore: boolean
  loadError: boolean
  loadErrorMessage: string
  observerRef: RefObject<HTMLDivElement | null>
  searchQuery: string
  onSearchChange: (query: string) => void
  onOpenChange: (open: boolean) => void
  onSwitchSession: (sessionId: string, resumable: boolean) => void
  onDeleteSession: (sessionId: string) => void
  onDeleteSessions: (sessionIds: string[]) => void
  onRenameSession: (sessionId: string, title: string) => void
}

export function SessionHistoryMenu({
  sessions,
  activeSessionId,
  hasMore,
  loadError,
  loadErrorMessage,
  observerRef,
  searchQuery,
  onSearchChange,
  onOpenChange,
  onSwitchSession,
  onDeleteSession,
  onDeleteSessions,
  onRenameSession,
}: SessionHistoryMenuProps) {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const [editingId, setEditingId] = useState<string | null>(null)
  const [renameValue, setRenameValue] = useState("")

  const handleOpenChange = (next: boolean) => {
    setOpen(next)
    if (!next) {
      setSelected(new Set())
      setEditingId(null)
    }
    onOpenChange(next)
  }

  const toggleSelected = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(id)) {
        next.delete(id)
      } else {
        next.add(id)
      }
      return next
    })
  }

  const handleBulkDelete = () => {
    onDeleteSessions(Array.from(selected))
    setSelected(new Set())
  }

  const startRename = (session: SessionSummary) => {
    setEditingId(session.id)
    setRenameValue(session.title)
  }

  const commitRename = () => {
    if (editingId) {
      const title = renameValue.trim()
      if (title) {
        onRenameSession(editingId, title)
      }
    }
    setEditingId(null)
  }

  const handleSwitch = (session: SessionSummary) => {
    if (editingId === session.id) {
      return
    }
    setOpen(false)
    onSwitchSession(session.id, session.resumable)
  }

  return (
    <DropdownMenu open={open} onOpenChange={handleOpenChange}>
      <DropdownMenuTrigger asChild>
        <Button variant="secondary" size="sm" className="h-9 gap-2">
          <IconHistory className="size-4" />
          <span className="hidden sm:inline">{t("chat.history")}</span>
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-80">
        <div className="border-border/60 border-b p-2">
          <input
            value={searchQuery}
            onChange={(e) => onSearchChange(e.target.value)}
            onKeyDown={(e) => e.stopPropagation()}
            placeholder={t("chat.historySearch")}
            className="border-border/60 bg-background focus:border-foreground/40 h-8 w-full rounded-md border px-2 text-sm outline-none"
          />
        </div>
        {selected.size > 0 && (
          <div className="border-border/60 flex items-center justify-between border-b px-3 py-1.5">
            <span className="text-muted-foreground text-xs">
              {t("chat.selectedCount", { count: selected.size })}
            </span>
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:bg-destructive/10 h-7 gap-1 text-xs"
              onClick={handleBulkDelete}
            >
              <IconTrash className="size-3.5" />
              {t("chat.deleteSelected")}
            </Button>
          </div>
        )}
        <ScrollArea className="max-h-[300px]">
          {loadError && (
            <DropdownMenuItem disabled>
              <span className="text-destructive text-xs">
                {loadErrorMessage}
              </span>
            </DropdownMenuItem>
          )}
          {sessions.length === 0 && !loadError ? (
            <DropdownMenuItem disabled>
              <span className="text-muted-foreground text-xs">
                {t("chat.noHistory")}
              </span>
            </DropdownMenuItem>
          ) : (
            sessions.map((session) => (
              <DropdownMenuItem
                key={session.id}
                onSelect={(event) => event.preventDefault()}
                className={`group my-0.5 flex items-center gap-2 ${
                  session.id === activeSessionId ? "bg-accent" : ""
                }`}
              >
                <input
                  type="checkbox"
                  checked={selected.has(session.id)}
                  onChange={() => toggleSelected(session.id)}
                  onClick={(e) => e.stopPropagation()}
                  aria-label={session.title}
                  className="accent-primary size-3.5 shrink-0 cursor-pointer"
                />
                <div
                  className="min-w-0 flex-1 cursor-pointer"
                  onClick={() => handleSwitch(session)}
                >
                  {editingId === session.id ? (
                    <input
                      autoFocus
                      value={renameValue}
                      onChange={(e) => setRenameValue(e.target.value)}
                      onKeyDown={(e) => {
                        e.stopPropagation()
                        if (e.key === "Enter") {
                          commitRename()
                        }
                        if (e.key === "Escape") {
                          setEditingId(null)
                        }
                      }}
                      onClick={(e) => e.stopPropagation()}
                      className="border-border/60 bg-background h-7 w-full rounded border px-1.5 text-sm outline-none"
                    />
                  ) : (
                    <>
                      <span className="line-clamp-1 block text-sm font-medium">
                        {session.title}
                        {!session.resumable && (
                          <span className="text-muted-foreground ml-1 text-xs">
                            · {t("chat.readOnly")}
                          </span>
                        )}
                      </span>
                      <span className="text-muted-foreground text-xs">
                        {t("chat.messagesCount", {
                          count: session.message_count,
                        })}{" "}
                        · {dayjs(session.updated).fromNow()}
                      </span>
                    </>
                  )}
                </div>
                {editingId === session.id ? (
                  <div className="flex shrink-0 items-center gap-0.5">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("chat.saveRename")}
                      className="h-6 w-6"
                      onClick={(e) => {
                        e.stopPropagation()
                        commitRename()
                      }}
                    >
                      <IconCheck className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("chat.cancelRename")}
                      className="h-6 w-6"
                      onClick={(e) => {
                        e.stopPropagation()
                        setEditingId(null)
                      }}
                    >
                      <IconX className="h-4 w-4" />
                    </Button>
                  </div>
                ) : (
                  <div className="flex shrink-0 items-center gap-0.5 opacity-0 transition-opacity group-hover:opacity-100">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("chat.renameSession")}
                      className="text-muted-foreground h-6 w-6"
                      onClick={(e) => {
                        e.stopPropagation()
                        startRename(session)
                      }}
                    >
                      <IconPencil className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label={t("chat.deleteSession")}
                      className="text-muted-foreground hover:bg-destructive/10 hover:text-destructive h-6 w-6"
                      onClick={(e) => {
                        e.stopPropagation()
                        onDeleteSession(session.id)
                      }}
                    >
                      <IconTrash className="h-4 w-4" />
                    </Button>
                  </div>
                )}
              </DropdownMenuItem>
            ))
          )}
          {hasMore && sessions.length > 0 && (
            <div ref={observerRef} className="py-2 text-center">
              <span className="text-muted-foreground animate-pulse text-xs">
                {t("chat.loadingMore")}
              </span>
            </div>
          )}
        </ScrollArea>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
