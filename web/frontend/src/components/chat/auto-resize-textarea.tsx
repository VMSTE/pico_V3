import { forwardRef, useRef, useEffect, useState, useCallback } from "react"

interface AutoResizeTextareaProps {
  value: string
  onChange: (value: string) => void
  onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void
  placeholder?: string
  disabled?: boolean
  className?: string
  minRows?: number
  maxRows?: number
}

/**
 * Оптимизированный textarea с авто-высотой без layout thrashing.
 *
 * Использует:
 * - Debounce (150ms) для resize операций
 * - Сброс высоты перед измерением
 * - Одно измерение за кадр через requestAnimationFrame
 * - Кэширование стилей для избежания принудительных reflow
 */
export const AutoResizeTextarea = forwardRef<HTMLTextAreaElement, AutoResizeTextareaProps>(
  (
    {
      value,
      onChange,
      onKeyDown,
      placeholder,
      disabled = false,
      className = "",
      minRows = 1,
      maxRows = 8,
    },
    externalRef,
  ) => {
    const internalRef = useRef<HTMLTextAreaElement>(null)
    const hiddenRef = useRef<HTMLTextAreaElement>(null)
    const [isComposing, setIsComposing] = useState(false)

    // Объединяем внешний ref (если передан) с внутренним
    const textareaRef = (el: HTMLTextAreaElement | null) => {
      internalRef.current = el
      if (typeof externalRef === "function") {
        externalRef(el)
      } else if (externalRef) {
        externalRef.current = el
      }
    }

    // Debounce таймер
    const resizeTimeoutRef = useRef<number | null>(null)
    const rafIdRef = useRef<number | null>(null)

    /**
     * Измеряет высоту через скрытый textarea с такими же стилями.
     * Это избегает принудительного reflow на видимом элементе.
     */
    const measureHeight = useCallback(() => {
      const textarea = internalRef.current
      const hidden = hiddenRef.current

      if (!textarea || !hidden) return

      // Копируем критические стили
      const styles = window.getComputedStyle(textarea)

      // Важно: только эти стили влияют на высоту
      hidden.style.font = styles.font
      hidden.style.fontSize = styles.fontSize
      hidden.style.fontFamily = styles.fontFamily
      hidden.style.fontWeight = styles.fontWeight
      hidden.style.letterSpacing = styles.letterSpacing
      hidden.style.lineHeight = styles.lineHeight
      hidden.style.padding = styles.padding
      hidden.style.paddingTop = styles.paddingTop
      hidden.style.paddingBottom = styles.paddingBottom
      hidden.style.border = styles.border
      hidden.style.borderTop = styles.borderTop
      hidden.style.borderBottom = styles.borderBottom
      hidden.style.boxSizing = styles.boxSizing
      hidden.style.width = `${textarea.offsetWidth}px`

      // Скрываем, но оставляем в DOM для правильного измерения
      hidden.style.visibility = "hidden"
      hidden.style.position = "absolute"
      hidden.style.top = "-9999px"
      hidden.style.left = "-9999px"

      // Устанавливаем значение и измеряем
      hidden.value = value || " "
      let height = hidden.scrollHeight

      // Применяем ограничения minRows/maxRows
      const lineHeight = parseInt(styles.lineHeight, 10) || 20
      const minHeight = minRows * lineHeight
      const maxHeight = maxRows * lineHeight

      height = Math.max(height, minHeight)
      height = Math.min(height, maxHeight)

      // Применяем высоту к видимому textarea
      textarea.style.height = `${height}px`
    }, [value, minRows, maxRows])

    /**
     * Отложенный resize с debounce и requestAnimationFrame.
     * Избегает множественных reflow при быстром вводе.
     */
    const scheduleResize = useCallback(() => {
      if (resizeTimeoutRef.current) {
        clearTimeout(resizeTimeoutRef.current)
      }

      // Debounce: ждем 150ms после последнего изменения
      resizeTimeoutRef.current = window.setTimeout(() => {
        if (rafIdRef.current) {
          cancelAnimationFrame(rafIdRef.current)
        }

        // Batch в следующем кадре
        rafIdRef.current = requestAnimationFrame(() => {
          measureHeight()
        })
      }, 150)
    }, [measureHeight])

    // Сброс высоты перед каждым изменением (важно!)
    const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
      const newValue = e.target.value

      // Сбрасываем высоту перед изменением значения
      if (internalRef.current) {
        internalRef.current.style.height = "auto"
      }

      onChange(newValue)
      scheduleResize()
    }

    // Измеряем при монтировании и изменении value
    useEffect(() => {
      // Не измеряем во время composition (IME)
      if (isComposing) return

      measureHeight()

      return () => {
        if (resizeTimeoutRef.current) {
          clearTimeout(resizeTimeoutRef.current)
        }
        if (rafIdRef.current) {
          cancelAnimationFrame(rafIdRef.current)
        }
      }
    }, [value, measureHeight, isComposing])

    // Обработчики для IME (иероглифы, составные символы)
    const handleCompositionStart = () => setIsComposing(true)
    const handleCompositionEnd = (e: React.CompositionEvent<HTMLTextAreaElement>) => {
      setIsComposing(false)
      // После завершения composition нужно обновить высоту
      if (internalRef.current) {
        internalRef.current.style.height = "auto"
      }
      onChange(e.currentTarget.value)
      scheduleResize()
    }

    return (
      <>
        {/* Скрытый textarea для измерения */}
        <textarea
          ref={hiddenRef}
          value={value}
          readOnly
          aria-hidden="true"
          style={{
            position: "absolute",
            top: "-9999px",
            left: "-9999px",
            visibility: "hidden",
            pointerEvents: "none",
            whiteSpace: "pre-wrap",
            wordWrap: "break-word",
          }}
        />

        {/* Основной textarea */}
        <textarea
          ref={textareaRef}
          value={value}
          onChange={handleChange}
          onKeyDown={onKeyDown}
          onCompositionStart={handleCompositionStart}
          onCompositionEnd={handleCompositionEnd}
          placeholder={placeholder}
          disabled={disabled}
          className={className}
          rows={minRows}
          style={{
            overflowY: "auto",
            resize: "none",
          }}
          // Важно: эти свойства помогают браузеру
          wrap="off"
          spellCheck={false}
        />
      </>
    )
  },
)

AutoResizeTextarea.displayName = "AutoResizeTextarea"