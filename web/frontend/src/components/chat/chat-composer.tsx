import { IconArrowUp, IconFile, IconPhotoPlus, IconX } from "@tabler/icons-react"
import { useEffect, useMemo, useRef, useState } from "react"
import type { KeyboardEvent } from "react"
import { useTranslation } from "react-i18next"
import TextareaAutosize from "react-textarea-autosize"

import { ContextUsageRing } from "@/components/chat/context-usage-ring"
import { Button } from "@/components/ui/button"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { fetchCommands, type CommandInfo } from "@/api/self-update"
import { cn } from "@/lib/utils"
import type { ChatAttachment, ContextUsage } from "@/store/chat"

export type ChatInputDisabledReason =
  | "gatewayUnknown"
  | "gatewayStarting"
  | "gatewayRestarting"
  | "gatewayStopping"
  | "gatewayStopped"
  | "gatewayError"
  | "websocketConnecting"
  | "websocketDisconnected"
  | "websocketError"
  | "noDefaultModel"

interface ChatComposerProps {
  input: string
  attachments: ChatAttachment[]
  onInputChange: (value: string) => void
  onAddImages: () => void
  onPasteFiles?: (files: File[]) => void
  onRemoveAttachment: (index: number) => void
  onSend: () => void
  onContextDetail?: () => void
  inputDisabledReason: ChatInputDisabledReason | null
  canSend: boolean
  contextUsage?: ContextUsage
}

export function ChatComposer({
  input,
  attachments,
  onInputChange,
  onAddImages,
  onPasteFiles,
  onRemoveAttachment,
  onSend,
  onContextDetail,
  inputDisabledReason,
  canSend,
  contextUsage,
}: ChatComposerProps) {
  const { t } = useTranslation()
  const canInput = inputDisabledReason === null
  const disabledMessage =
    inputDisabledReason === null
      ? null
      : t(`chat.disabledPlaceholder.${inputDisabledReason}`)
  const placeholder = disabledMessage ?? t("chat.placeholder")

  // D-AUDIT-105: slash-command palette
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const [commands, setCommands] = useState<CommandInfo[]>([])
  const [paletteDismissed, setPaletteDismissed] = useState(false)
  const [highlight, setHighlight] = useState(0)

  useEffect(() => {
    fetchCommands()
      .then(setCommands)
      .catch(() => {})
  }, [])

  const slashQuery =
    input.startsWith("/") && !input.includes(" ") ? input.slice(1) : null
  const filteredCommands = useMemo(() => {
    if (slashQuery === null) return []
    const q = slashQuery.toLowerCase()
    return commands.filter((c) => c.name.toLowerCase().startsWith(q))
  }, [commands, slashQuery])
  const paletteOpen =
    canInput &&
    slashQuery !== null &&
    !paletteDismissed &&
    filteredCommands.length > 0

  useEffect(() => {
    setHighlight(0)
  }, [slashQuery])
  useEffect(() => {
    if (slashQuery === null) setPaletteDismissed(false)
  }, [slashQuery])

  const pickCommand = (cmd?: CommandInfo) => {
    if (!cmd) return
    onInputChange(`/${cmd.name} `)
    setPaletteDismissed(true)
    textareaRef.current?.focus()
  }

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return
    if (paletteOpen) {
      if (e.key === "ArrowDown") {
        e.preventDefault()
        setHighlight((h) => (h + 1) % filteredCommands.length)
        return
      }
      if (e.key === "ArrowUp") {
        e.preventDefault()
        setHighlight(
          (h) => (h - 1 + filteredCommands.length) % filteredCommands.length,
        )
        return
      }
      if (e.key === "Escape") {
        e.preventDefault()
        setPaletteDismissed(true)
        return
      }
      if ((e.key === "Enter" && !e.shiftKey) || e.key === "Tab") {
        e.preventDefault()
        pickCommand(
          filteredCommands[Math.min(highlight, filteredCommands.length - 1)],
        )
        return
      }
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault()
      onSend()
    }
  }

  const handlePaste = (e: React.ClipboardEvent<HTMLTextAreaElement>) => {
    if (!onPasteFiles) return
    const files: File[] = []
    for (const item of Array.from(e.clipboardData?.items ?? [])) {
      if (item.kind === "file") {
        const file = item.getAsFile()
        if (file) files.push(file)
      }
    }
    if (files.length > 0) {
      e.preventDefault()
      onPasteFiles(files)
    }
  }

  return (
    <div className="before:bg-background pointer-events-none relative z-10 -mt-[24px] shrink-0 overflow-y-auto px-4 pb-[calc(1rem+env(safe-area-inset-bottom))] [scrollbar-gutter:stable] before:pointer-events-none before:absolute before:inset-x-0 before:top-[24px] before:bottom-0 before:content-[''] md:px-8 md:pb-8 lg:px-24 xl:px-48">
      <div className="bg-card border-border/60 pointer-events-auto relative mx-auto flex max-w-[1000px] flex-col rounded-2xl border p-3 shadow-sm">
        {attachments.length > 0 && (
          <div className="mb-3 flex flex-wrap gap-2 px-2">
            {attachments.map((attachment, index) => (
              <div
                key={`${attachment.url}-${index}`}
                className="bg-background relative h-20 w-20 overflow-hidden rounded-xl border"
              >
                {attachment.type === "image" ? (
                  <img
                    src={attachment.url}
                    alt={attachment.filename || t("chat.uploadedImage")}
                    className="h-full w-full object-cover"
                  />
                ) : (
                  <div className="flex h-full w-full flex-col items-center justify-center gap-1 p-1">
                    <IconFile className="text-muted-foreground size-6" />
                    <span className="text-muted-foreground w-full truncate px-1 text-center text-[10px]">
                      {attachment.filename || "file"}
                    </span>
                  </div>
                )}
                <button
                  type="button"
                  onClick={() => onRemoveAttachment(index)}
                  className="bg-background/85 text-foreground absolute top-1 right-1 inline-flex h-6 w-6 items-center justify-center rounded-full border shadow-sm transition hover:bg-white"
                  aria-label={t("chat.removeImage")}
                  title={t("chat.removeImage")}
                >
                  <IconX className="h-3.5 w-3.5" />
                </button>
              </div>
            ))}
          </div>
        )}

        {paletteOpen && (
          <div className="bg-popover border-border/60 mb-2 max-h-64 overflow-y-auto rounded-xl border p-1 shadow-lg">
            {filteredCommands.map((cmd, i) => (
              <button
                key={cmd.name}
                type="button"
                onMouseDown={(e) => {
                  e.preventDefault()
                  pickCommand(cmd)
                }}
                onMouseEnter={() => setHighlight(i)}
                className={cn(
                  "flex w-full items-baseline gap-2 rounded-lg px-3 py-2 text-left",
                  i === highlight && "bg-accent",
                )}
              >
                <span className="text-foreground font-mono text-sm">
                  /{cmd.name}
                </span>
                <span className="text-muted-foreground truncate text-xs">
                  {cmd.description}
                </span>
              </button>
            ))}
          </div>
        )}

        <TextareaAutosize
          ref={textareaRef}
          value={input}
          onChange={(e) => onInputChange(e.target.value)}
          onKeyDown={handleKeyDown}
          onPaste={handlePaste}
          placeholder={placeholder}
          disabled={!canInput}
          title={disabledMessage || undefined}
          className={cn(
            "placeholder:text-muted-foreground/50 max-h-[200px] min-h-[64px] resize-none border-0 bg-transparent px-2 py-1 text-[15px] shadow-none transition-colors focus-visible:ring-0 focus-visible:outline-none dark:bg-transparent",
            !canInput && "cursor-not-allowed",
          )}
          minRows={1}
          maxRows={8}
        />

        <div className="mt-2 flex items-center justify-between px-1">
          <div className="flex items-center gap-1">
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className="text-muted-foreground hover:text-foreground h-8 w-8 rounded-full"
              onClick={onAddImages}
              disabled={!canInput}
              aria-label={t("chat.attachImage")}
              title={t("chat.attachImage")}
            >
              <IconPhotoPlus className="size-4" />
            </Button>
          </div>

          <div className="flex items-center gap-1.5">
            {contextUsage && (
              <ContextUsageRing usage={contextUsage} onDetailClick={onContextDetail} />
            )}
            {canInput ? (
              <Tooltip delayDuration={700}>
                <TooltipTrigger asChild>
                  <span tabIndex={!canSend ? 0 : undefined}>
                    <Button
                      type="button"
                      size="icon"
                      className="size-8 rounded-full bg-violet-500 text-white transition-transform hover:bg-violet-600 active:scale-95"
                      onClick={onSend}
                      disabled={!canSend}
                      aria-label={t("chat.sendMessage")}
                    >
                      <IconArrowUp className="size-4" />
                    </Button>
                  </span>
                </TooltipTrigger>
                <TooltipContent
                  className="border-border/70 bg-muted text-foreground border text-center whitespace-pre-line shadow-lg shadow-black/10 dark:shadow-black/30"
                  arrowClassName="bg-muted fill-muted"
                >
                  {t("chat.sendHint")}
                </TooltipContent>
              </Tooltip>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}
