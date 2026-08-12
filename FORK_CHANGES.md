## Волна 51 — MCP API для Web UI: probe в pkg/mcp (D-AUDIT-84) · 12 авг 2026

- pkg/mcp/probe.go — exported ProbeServer (connect + tool count): переиспользуется CLI и Web.
- web/backend/api/mcp.go — GET/PUT/DELETE /api/mcp/servers + POST /{name}/test; секреты отдаются только именами ключей; PUT включает tools.mcp, DELETE последнего сервера выключает (как CLI).
- cmd/picoclaw/internal/mcp/helpers.go — defaultServerProbe делегирует в pkg/mcp (убрано дублирование).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 49 — Телеметрия спутников + component=subturn + latency (D-AUDIT-82) · 11 авг 2026

- **RecordSatelliteLLM** (pkg/pika/telemetry.go, NEW): fire-and-forget запись вызова спутника в request_log — component, direction, model, токены из usage, response_ms. nil-safe.
- **Спутники подключены** (D-83 наконец исполнено): archivarius (build_prompt, session из currentSessionKey), atomizer (atomize, sessionID протащен через callWithRetry→callLLM), reflexor (review), mcp_guard (guard_audit — покрывает и skill audit, тот же caller).
- **pipeline_llm.go**: component="subturn" при ts.depth>0 (суб-агенты spawn больше не маскируются под main); LatencyMs = время retry-цикла → оживает P95-латентность в аналитике.
- **Тест** TestRecordSatelliteLLM.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-GREEN, lint 0 issues.

## Волна 48 — Продюсеры телеметрии: task_tag, chain_id, tool outcomes (D-AUDIT-81) · 11 авг 2026

- **task_tag**: pipeline_llm.go читает кэшированный FOCUS Архивариуса (GetCachedFocus через GetArchivist, type-assertion — без Архивариуса тег пустой) → request_log. 0 LLM, как предписывает архитектура (notion-693 §5B).
- **chain_id / chain_position**: UUID цепочки генерируется lazy на первом LLM-вызове хода (turnState.ensureChainID), позиция = iteration. Отклонение от D-51 зафиксировано: одиночные вызовы тоже получают chain длиной 1 (аналитика фильтрует MAX(position)>1).
- **tool_calls_success/failed**: RecordLLMCall возвращает id строки; pipeline_execute.go после каждого реального исполнения инкрементирует счётчики (BotMemory.UpdateRequestLogToolResults, UPDATE по id). Hook-respond/deny пути не считаются — только реальные выполнения.
- **Тесты**: TestRecordLLMCall_TaskChain, TestUpdateRequestLogToolResults.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-GREEN, lint 0 issues.

## Волна 47 — Пакет фиксов №3: anthropic-sdk 1.50.2 + хвост panic-recovery (D-AUDIT-80) · 11 авг 2026

- **anthropic-sdk-go 1.26.0 → 1.50.2**: v1.50.2 удалил NewThinkingConfigAdaptiveParam() — provider.go переведён на структурный литерал &anthropic.ThinkingConfigAdaptiveParam{} (паттерн подтверждён message_test.go самого SDK; Type сериализуется автоматически).
- **pipeline_llm.go:356/358 — последние 2 горутины без recover** (publishPicoReasoning, handleReasoning; многострочные вызовы, не взятые механическим патчем D-AUDIT-79): обёрнуты defer recover → logger.RecoverPanicNoExit. Инвентарь panic-recovery теперь закрыт полностью.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-GREEN, lint 0 issues.

## Волна 46 — Пакет фиксов №2: panic-recovery на боевых горутинах (D-AUDIT-79) · 11 авг 2026

- **64 точки запуска горутин получили defer recover → logger.RecoverPanicNoExit**: паника пишется в panic-лог, процесс выживает (раньше — падение всего процесса).
- Классы: manager.go диспетчеры/воркеры (10), ядро агента hooks/hook_process/pipeline_finalize (9), боевые петли всех каналов (~42), pkg/mcp (3).
- Пропущены осознанно: 6 точек с существующим recover (agent.go ×2, subturn.go ×2, discord/voice.go ×2), pipeline_llm.go:356/358 (многострочные вызовы — отдельная задача).
- Честно: recover ≠ рестарт — упавшая петля мертва до рестарта процесса, но процесс живёт и причина в panic-логе. Супервизор с автоперезапуском — отдельный дизайн.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-TARGETED-GREEN, lint 0 issues.

## Волна 45 — Пакет фиксов №1 после ревизии upstream (D-AUDIT-78) · 10 авг 2026

- **ISATAP SSRF закрыт** (зеркало upstream #3143, реализация по RFC 5214): `isPrivateOrRestrictedIP` в pkg/tools/integration/web.go теперь проверяет ISATAP-литералы — маркер `5efe` в байтах [10:12], встроенный IPv4 в [12:16], рекурсивная проверка. До этого адрес вида `::5efe:192.168.1.1` проходил гард.
- **Депы до уровня v0.3.1**: modernc.org/sqlite 1.48.2→1.53.0, telego 1.8.0→1.10.0 (+ транзитивные fasthttp/libc). anthropic-sdk-go НЕ поднят: v1.50.2 удалил NewThinkingConfigAdaptiveParam, который использует наш provider.go — отдельная задача с адаптацией.
- **Аудит panic-recovery** (без патчей): 70 точек запуска горутин в pkg/agent+pkg/channels, recover() только в 3 файлах — сигнал к отдельной задаче.
- **Тесты**: таблица isPrivateOrRestrictedIP += 4 ISATAP-кейса.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN (33 пакета), RACE-TARGETED-GREEN, lint 0 issues.

## Волна 44 — Харденинг /api/update + оценка CVE upstream (D-AUDIT-76) · 10 авг 2026

- Оценка двух уязвимостей upstream по нашему коду: **CVE-2026-36045** (ExecTool, ≤v0.1.2) — закрыта наследием (расширенный denylist ~22 паттерна + symlink-защита + наши Confirmation Gate/RAD); **CVE-2026-6987** (Web Launcher, «no known fixed») — вредоносный паттерн в нашем коде отсутствует (restart без shell, autostart через shellQuote, auth + IP-allowlist + loopback дефолт).
- Найден и закрыт реальный стык: **/api/update** принимал произвольные URL релиза и имя бинаря из POST → произвольный бинарь с чужого хоста через selfupdate.
- **pkg/updater/validate.go** (NEW) — ValidateReleaseSource: URL пустой ИЛИ prefix официального release API; имя бинаря без path separators/traversal. Нарушение → 400 до загрузки.
- **web/backend/api/update.go** — вызов валидации до UpdateSelfFromRelease.
- **Тесты**: pkg/updater (9 кейсов) + web/backend/api (foreign URL → 400, traversal binary → 400).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, lint 0 issues.

## Волна 43 — registry_write: последний код-элемент архитектуры (D-AUDIT-74, ТЗ-v2-3f) · 10 авг 2026

- Закрыт пустой шаг 6 протокола скриптов (§5.3): модель может писать в постоянный реестр. AGENT.md обещал registry_write — тул не существовал, теперь существует.
- **pkg/pika/registry_write_tool.go** (NEW) — RegistryWriteTool: stateless singleton, toolshared.Tool, обёртка над RegistryHandler (ТЗ-v2-1c). kind ∈ {runbook, script, snapshot, correction_rule}; Go — единственный писатель (валидация в RegistryHandler); data clamp 64KB; tags только массив строк; результат JSON {status: created|updated, kind, key}.
- **pkg/agent/instance.go** — регистрация в BRAIN-блоке (always-on, IsCore=true) рядом с search_memory и discover_tools.
- **Тесты**: все 12 из спеки ТЗ-v2-3f (create/update ×4 kind, невалидные входы, tags, interface compliance).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, lint 0 issues.
- После этого PR архитектура v3 достроена по коду целиком; остаётся боевая проверка (активация сервера).

## Волна 42 — Per-server RPM для MCP-вызовов (D-AUDIT-73) · 10 авг 2026

- Конфиг security.mcp.per_server_rpm (+ per-server rpm в ACL) существовал с D-SEC-v3, но потребителя не было. Теперь лимиты применяются.
- **Механизм — upstream-примитив**: golang.org/x/time/rate token bucket (тот же, что в channels и providers). Ничего не писали с нуля.
- **pkg/mcp/manager.go** — SetServerRPM + allowCall + проверка в CallTool. Неблокирующе (Allow, не Wait): превышение → честная ошибка модели «лимит N/мин, подожди» — зацикленный агент обрывается на 61-м вызове, ход не подвисает. Reconnect не двоит счётчик.
- **pkg/agent/agent_mcp.go** — wiring: лимиты из mapMCPServerPolicies при подключении серверов.
- **Тест**: burst проходит, превышение отклоняется, снятие лимита работает, сервер без лимита не затронут.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, lint 0 issues.

## Волна 41 — Rug Pull Guard event-driven (D-AUDIT-72, D-SEC-v2) · 10 авг 2026

- Подмена описаний MCP-тулов посреди сессии теперь ловится строго по событию, как задумано в D-SEC-v2: handler на notifications/tools/list_changed (нативный ClientOptions.ToolListChangedHandler в go-sdk) → tools/list заново → hash-diff (CheckRugPull) → re-audit изменённых/новых через Guard → malicious → Unregister из реестров всех агентов + уведомление founder'а. Никаких тикеров/опросов (первый вариант с polling 15 мин отвергнут — антипаттерн).
- **Flood-guard** (D-SEC-v2): >2 list_changed в час от сервера = suspicious → события игнорируются, founder получает alert.
- **pkg/mcp/manager.go** — SetOnToolsListChanged (подписка), ClientOptions.ToolListChangedHandler в connectServer, RefreshServerTools (перечитать список по событию).
- **pkg/tools/registry.go** — Unregister (деактивация тула на ходу). **pkg/tools/integration/mcp_tool.go** — экспорт MCPToolName (рефактор Name). **pkg/tools/integration_facade.go** — re-export.
- **pkg/pika/mcp_security.go** — HasToolHashes (нет эталона → первая сверка только записывает его).
- **pkg/agent/rug_pull.go** (NEW) — recordListChanged (flood), notifyManager, handleToolsListChanged, rugPullRecheck (ядро). Новые тулы при list_changed проходят ACL + Guard, как при старте. Прошедшие changed пере-регистрируются (описание обновляется).
- **pkg/agent/mcp_guard_gate.go + mcp_acl_gate.go** — pipeline персистится на AgentLoop (mcpSecurityPipeline get-or-create): хэши переживают вызовы.
- **Тесты**: flood-limit, flood-alert founder'у, baseline-record, malicious → unregister + notify. Плюс Unregister тест в pkg/tools.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-TARGETED-GREEN, lint 0 issues.

## Волна 40 — Guard-аудит описаний MCP-тулов при регистрации + уведомление founder'а (D-AUDIT-71) · 10 авг 2026

- После ACL (волна 38, проверка ИМЕНИ) добавлена вторая половина ворот: Guard читает ОПИСАНИЯ разрешённых тулов при подключении сервера (startup_audit, один пакетный вызов на сервер). Вердикт malicious/dangerous → тул не регистрируется.
- **Уведомление founder'а**: блокировка шлёт сообщение менеджеру через NewManagerSender — сервер, тулы, причины + инструкция. Молча не блокируем.
- **Решение за пользователем**: новое поле конфига security.mcp.servers.<имя>.guard_except — founder вручную разрешает тул после прочтения; при следующем старте регистрируется.
- Fail-open при ошибке Guard (warn-лог): упавший сторож не останавливает систему.
- **pkg/agent/mcp_guard_gate.go** (NEW) — mcpToolSummary, auditToolDefsWithGuard (ядро), applyGuardExcept, guardAuditMCPTools, notifyManagerGuardBlock. Использует продовый guardLLMCaller из волны 39.
- **pkg/config/config_pika.go** — MCPServerACLConfig += GuardExcept.
- **Тесты**: block-with-reason, fail-open на битом ответе Guard, override через guard_except, уведомление менеджеру через bus.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-TARGETED-GREEN, lint 0 issues.
- Итог темы инструментов: все три источника (MCP, скиллы, хуки) проходят полный конвейер аудита.

## Волна 39 — Контентный аудит скиллов через MCP Guard (D-AUDIT-70) · 10 авг 2026

- Скиллы проходили только модерацию реестра (IsMalwareBlocked) — содержимое SKILL.md никто не читал. Теперь при установке текст скилла уходит Guard-агенту (startup_audit, скилл как pseudo-tool), вердикт malicious/dangerous → каталог удаляется, бэкап восстанавливается. Fail-open при ошибке аудита (доступность > ложные блоки).
- **pkg/tools/integration/skills_install.go** — интерфейс SkillAuditor + SetAuditor + вызов аудита после валидации, до записи метаданных. Интерфейс в integration-пакете: pkg/pika импортирует pkg/tools, обратный импорт = цикл.
- **pkg/agent/skill_guard.go** (NEW) — продовый guardLLMCaller (provider.Chat, модель и таймаут из Guard-конфига) + skillGuardAuditor + newSkillGuardAuditor. До этого MCPGuardLLMCaller существовал только как тестовый мок — Guard-аудит был мёртвым кодом. Теперь этот же caller можно подключить и к MCP StartupAudit (отдельная задача).
- **pkg/agent/agent_init.go** — аудитор подключается при регистрации install_skill.
- **Тесты**: TestSkillGuardAuditor_BlocksMalicious / AllowsSafe (pkg/agent), TestInstallSkillTool_AuditorBlocksMalicious / AuditorAllows (integration).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-TARGETED-GREEN, lint 0 issues.

## Волна 38 — Единые ворота аудита тулов (D-AUDIT-69) · 10 авг 2026

- Все источники тулов теперь проходят проверку до попадания в реестр.
- **pkg/agent/agent_mcp.go** — per-server ACL (FilterAllowedTools, deny-by-default D-SEC-v3) вызывается ДО Register/RegisterHidden: сервер без записи в security.mcp.servers не получает ни одного тула, warning с подсказкой в логе. Раньше conn.Tools регистрировались напрямую — ACL был мёртвым кодом.
- **pkg/agent/mcp_acl_gate.go** (NEW) — gatedMCPToolNames: ACL-фильтр по именам; pipeline строится из конфига на месте, если не была подключена ранее.
- **pkg/agent/pipeline_execute.go** — respond-хук для ЗАРЕГИСТРИРОВАННОГО тула теперь требует ApproveTool (раньше hook мог подменить результат любого тула, включая exec, в обход одобрения). Плагинные незарегистрированные тулы — поведение без изменений.
- **Скиллы** — без изменений: модерация реестра (IsMalwareBlocked) уже работает на всех 3 путях установки.
- **Тесты**: TestGatedMCPToolNames_ACL (allowlist + deny unknown), TestAgentLoop_HookRespond_RegisteredToolRequiresApproval.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, RACE-TARGETED-GREEN, lint 0 issues.

## Волна 37 — Честный вход Архивариуса (D-AUDIT-60) · 10 авг 2026

- Архивариус выбирал инструменты вслепую: каталог приходил только именами (agent.Tools.List()), а промпт требовал «сопоставь задачу с description». Хуже: сообщение пользователя вообще не передавалось (Message пуст) — бриф строился без вопроса.
- **pkg/pika/interfaces.go** — ArchivistInput += ActivePlan, MaxRecommendedTools, MaxRecommendedSkills; комментарий-граница: каталог = тулы ОСНОВНОЙ модели, search_context сюда не входит.
- **pkg/agent/context_pika.go** — передаём Message (req.CurrentMessage), каталог через GetSummaries() (имя+описание), ActivePlan (cm.ExtractActivePlan), лимиты из ToolSelectionConfig.
- **pkg/pika/archivist.go** — Config-секция входа += session_id, active_plan, max_recommended_tools/skills.
- **workspace/prompts/archivist_build.md** — убран search_context из CORE основной модели (это собственный тул Архивариуса); поля конфига переименованы под реальные (max_recommended_*).
- **Тест** TestBuildUserMessage_HonestInput (NEW файл): все поля доезжают, active_plan опускается когда пуст.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 36 — Петля диагностики спец-агентов замкнута (D-AUDIT-67, ч.2) · 9 авг 2026

- Спаны спец-агентов получили trace_id и честный error-статус: раньше закрывались «ok» даже при сбое. Дефер через context.WithoutCancel (у Архивариуса ctx с таймаутом, defer идёт после cancel).
- **archivist.go / atomizer.go / reflector.go** — defer: сбой → Diagnose (атрибуция по трассе + поиск повторов за 7д) → при 2+ похожих CreateCR (уведомление менеджеру); успех → IncrementVerified (правила копят подтверждения → статус verified).
- **reflector.go** — weekly-режим вызывает ReviewCRs: проверенные 7+ дней → promoted, активные 30+ дней без подтверждений → deactivated.
- Исправление в ходе работы: компонент спана Рефлексора называется reflector, CR-компонент — reflexor (validCRComponents); разведены.
- **Тест** TestDiagnose_ErrorSpanAttribution: 3 error-спана → атрибуция + SuggestedCR.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 35 — Классификатор фидбека + режим «расследователь» Рефлексора (D-AUDIT-67) · 9 авг 2026

- **pkg/pika/feedback.go** — NEW: ClassifyFeedback — детерминированный Go-классификатор негативного фидбека (таксономия Don-Yehiya et al. 2024): correction / wrong / clarification / rephrase (пересечение слов ≥0.6). 0 LLM.
- **pkg/pika/botmemory.go** — NEW MarkFeedbackSignal (сигнал в messages.metadata последнего user-сообщения — закрывает D-85) + GetRecentFailEvents (fail-события за окно, все сессии или по сессии).
- **pkg/pika/reflector.go** — NEW режим reflectorInvestigate: выборка свежих атомов + блок fail-событий за 24ч (buildInvestigateContext); TriggerInvestigation — event-driven фоновый запуск с троттлингом 10 мин/сессия (НЕ горячий путь — F9-5). Investigate на пустой базе — тихий выход.
- **pkg/agent/pipeline_setup.go** — классификатор на персисте входящего сообщения: сигнал → metadata; wrong/correction/rephrase → TriggerInvestigation.
- **pkg/agent/pipeline_execute.go** — ошибка инструмента → TriggerInvestigation.
- **Тесты**: TestClassifyFeedback (8 кейсов), TestMarkFeedbackSignal (запись + изоляция сессий), TestGetRecentFailEvents, TestRunInvestigate_Empty, TestTriggerInvestigation_Throttle.
- Петля correction rules (Diagnose/IncrementVerified/ReviewCRs + error-статусы спанов) — следующим PR.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 34 — Деградация: живые уведомления менеджеру + честный блок в промпте (D-AUDIT-65) · 9 авг 2026

- Обе цепочки были заглушены в фабрике: модель всегда «здорова» (AlwaysHealthyProvider), менеджер не узнаёт никогда (progress=nil). Таблица перенаправлений (degradationInstruction) и ProgressObserver с троттлингом уже существовали — не хватало проводов.
- **pkg/agent/context_pika.go** — Telemetry получает живой ProgressNotifier (ProgressObserver поверх ManagerSender, адрес из health.reporting.manager_*); PikaContextManager получает telemetry как SystemStateProvider вместо заглушки.
- Эффект: деградация компонента → менеджеру уведомление (D-HRL) + в промпте основной модели блок DEGRADATION с инструкцией (archivist → search_memory и т.д., D-92, ТЗ-v2-2b). Восстановление → NotifyRecovery + блок исчезает.
- **Тест** TestDegradationBlock_FromTelemetry (NEW файл): пусто при здоровье → блок с search_memory при деградации archivist → пусто после recovery.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 33 — request_log пишет полные данные хода (D-AUDIT-64) · 9 авг 2026

- RecordLLMCall писал только сессию/модель/токены/цену/задержку — аналитика tools/tasks была пуста по построению (tool_calls_requested>0 никогда).
- **pkg/pika/telemetry.go** — RecordLLMParams += ToolCallsRequested/ToolNames/ReasoningTokens; маппинг в RequestLogRow (tool_names — JSON-массив).
- **pkg/agent/pipeline_llm.go** — конвейер передаёт счётчик и имена инструментов из exec.response.ToolCalls + reasoning-токены (оценка ~4 символа/токен, как в reasoning_log).
- Скоуп честно урезан при реализации: task_tag/chain_id НЕ имеют производителя в коде (отдельная задача), tool_calls_success/failed считаются после выполнения (отдельный шаг).
- **Тест** TestRecordLLMCall_FullFields: поля доезжают до request_log.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 32 — Писатель связей атом→инструмент (D-AUDIT-61) · 9 авг 2026

- atom_usage.invoked_tool_after/invoked_tool_result никогда не писались в проде → QueryCorrelatedTools всегда пуст, эффективность атомов в отчётах = 0%. До PR #77 записи ещё и отклонялись по FK (чинено D-AUDIT-63).
- **pkg/pika/botmemory.go** — NEW MarkAtomUsageToolAfter: UPDATE atom_usage текущего хода (последний trace_id для pika_session_id), первая запись побеждает (IS NULL).
- **pkg/agent/pipeline_execute.go** — вызов после каждого выполнения инструмента, рядом со скользящим окном здоровья. Пишет Go; ошибка записи логируется, ход не роняется.
- **Тест** TestMarkAtomUsageToolAfter: запись, first-write-wins, изоляция по ходам.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 31 — Корневой фикс пустых trace_spans и atom_usage (D-AUDIT-63) · 9 авг 2026

- DDL trace_spans разрешает status IN (ok,error,timeout,cancelled), а писатели слали "running"/"done" → SQLite молча отклонял INSERT (fire-and-forget). Каскад по FK: atom_usage.archivarius_span_id → atom_usage тоже отклонялся. В бою обе таблицы были пусты.
- **archivist.go / atomizer.go / reflector.go** — статусы приведены к разрешённым: старт "ok", завершение "ok" (захват "error" при сбоях — отдельным шагом).
- **botmemory_test.go** — регрессионный TestSpanStatusRegression: запись, error-статус легален, duration_ms вычисляется.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 30 — Trajectory-метрики задач: пишет Go, читает Рефлексор (D-AUDIT-62) · 9 авг 2026

- Блок EFFICIENCY в reflexor.md был мёртв дважды: метрики никто не писал (D-112 открыт), вход Рефлексора их не передавал. Решение: петля замкнута через Go, 0 участия модели в записи.
- **pkg/pika/atomizer.go** — NEW TrajectoryMetrics + computeTrajectoryMetrics + toolNameFromEventTags: при вставке атома Go досчитывает метрики по source_turns из уже загруженного чанка (tokens_used из messages.tokens; actual_calls/failed_calls/tool_sequence из событий по тегу tool:<name>; duration_ms из ts) и пишет в history JSON атома. 0 изменений DDL, 0 новых запросов.
- **pkg/pika/reflector.go** — reflectorAtomForLLM += trajectory_metrics; buildUserContent прикрепляет метрики из history (trajectoryMetricsFromHistory). Имена полей совпадают с промптом — reflexor.md не менялся, блок EFFICIENCY ожил.
- **Тесты**: TestComputeTrajectoryMetrics (считалка, изоляция по ходам), TestTrajectoryMetricsFromHistory (доставка + битые входы).
- D-146 частично отменён: анализ эффективности возвращён Рефлексору на Go-посчитанных данных; analytics.go (отчёты пользователю) не тронут.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues.

## Волна 29 — Журнал событий починен целиком (D-AUDIT-59) · 9 авг 2026

- Цепочка автособытий (D-54, D-SEC-MCP слой 5) была оборвана в трёх местах: адаптер передавал пустые операцию/сессию/ход (ключи никогда не совпадали, записано 0 событий за всю жизнь), обработчик собирался с пустыми таблицами, MCP-маппинги не вызывал никто.
- **pkg/agent/events.go** — ToolExecEndPayload += Operation.
- **pkg/agent/hook_pika.go** — адаптер передаёт настоящие операцию/сессию/ход (из payload + меты события).
- **pkg/agent/agent.go** — поле autoEvent на AgentLoop.
- **pkg/agent/context_pika.go** — обработчик собирается с живыми таблицами (BuildAutoEventConfig по включённым серверам), ValidateStartup при старте, handler в al.autoEvent.
- **pkg/pika/autoevent.go** — NEW BuildAutoEventConfig: MCP-маппинги + классы + BRAIN-классы; без серверов mcp-классы не сиротеют.
- **pkg/agent/pipeline_execute.go** — MCP-события mcp.<сервер>.call/call_fail/blocked прямым вызовом (сервер из имени <сервер>__<инструмент>); Operation заполняется в обоих emit-точках; помощники toolOperationArg/mcpServerFromToolName.
- **pkg/agent/rad_gate.go** — rad.blocked/warning → rad_anomaly/rad_warning в журнале.
- **pkg/pika/autoevent_test.go** — 2 сквозных теста: сборка без предупреждений + запись mcp_call/mcp_blocked/rad_anomaly в базу; табличный стиль (govet shadow чист).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN, golangci-lint 0 issues, deadcode: MCPAutoEvent* достижимы.

## Волна 28 — Защита от зацикливания подключена (D-AUDIT-58) · 9 авг 2026

- **pkg/agent/pipeline_execute.go** — после каждого выполнения инструмента: запись в TRAIL (имя + операция/превью аргументов + результат ≤100 симв. + OK + длительность) и прямой вызов `pika.CheckLoopDetection` — НЕ хук, safety net невыключаем (D-136a). Срабатывание → стоп хода (ToolControlBreak), сообщение в чат «Обнаружено зацикливание… loop_detected», уведомление менеджеру при настроенном health.reporting.manager_*. Критический факт из SCIP: Trail.Add до этого вызывался только из тестов — журнал создавался, но не кормился.
- **pkg/pika/trail_meta.go** — HasLoopDetection теперь сравнивает и Result+OK, не только имя+операцию (урок Gemini CLI #11002: та же команда с ДРУГИМ результатом = прогресс, не петля).
- **pkg/pika/loop_detector.go** — новая константа DefaultLoopDetectionThreshold = 3 (конфига нет by design: нельзя отключить).
- **pkg/pika/trail_meta_test.go** — новый тест TestTrailLoopDetection_ResultAware (разный результат → не петля; полное совпадение → петля; разный OK → не петля).
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN (pkg/pika + pkg/agent), golangci-lint 0 issues, deadcode: loop_detector.go больше не в списке (CheckLoopDetection достижим).

## Волна 27 — Закалка флаки-теста reasoning channel · 9 авг 2026

- **pkg/agent/agent_test.go** — `TestProcessMessage_PublishesReasoningContentToReasoningChannel`: страховочный бюджет ожидания асинхронной публикации reasoning поднят с 3с до 15с (+ комментарий, что это worst-case, не фиксированный сон). Причина флака: select по каналу просыпается мгновенно при приходе сообщения, но 3с не хватило нагруженному CI-раннеру (падение на 3.10с, PR #71, подтверждено флаком рестартом). Других фиксированных ожиданий на шине в тестах нет — проверено поиском.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, целевой тест ×5 зелёный, полный пакет pkg/agent зелёный.

## Волна 26 — Миграция slack-go v0.17.3 → v0.23.1 (Р-9, GO-2026-5410) · 8 авг 2026

- **go.mod / go.sum** — slack-go v0.17.3 → v0.23.1: фикс GO-2026-5410 (`NewSecretsVerifier` теперь отвергает пустой signing secret). Плюс транзитивные обновления через `go mod tidy`.
- **pkg/channels/slack/slack.go** — единственная точка дрейфа API: переименование из v0.18.0 `UploadFileV2Context` → `UploadFileContext` и `UploadFileV2Parameters` → `UploadFileParameters` (Slack закрыл `files.upload` 12.11.2025, суффикс «V2» снят). Комментарий у вызова синхронизирован.
- Остальные девять групп используемых API — без изменений: socketmode (New/RunContext/Events/Ack), slackevents (MessageEvent/AppMentionEvent), AuthTest, PostMessageContext + MsgOption*, AddReaction/RemoveReaction, slack.File, SlashCommand. Ломающие изменения v0.18–v0.23 (удаление IM struct, Info.Get*ByID, курсорная пагинация ListReactions) нас не касаются — мы их не используем.
- Гейты: GOFMT-CLEAN, BUILD-OK, VET-OK, TESTS-GREEN (все 16 пакетов каналов). govulncheck: 0 уязвимостей в коде, 0 в импортируемых пакетах, 1 в требуемых модулях (openpgp — не вызывается, решение по D-AUDIT-56).

# FORK_CHANGES — pico_V3

Tracker of all structural changes made in the Pika v3 fork vs upstream PicoClaw.
Each entry maps to a single wave/phase and its merged PR.

---

## Wave 0: Foundation (schema + config)

### [2026-05-01] feat(pika): add migrate.go — wave 0a

- **ТЗ:** ТЗ-v2-0a: migrate.go — Схема bot_memory.db
- **PR:** #1 (merged)
- **Files:**
  - `pkg/pika/migrate.go` — NEW: `Migrate(dbPath)` + `CurrentVersion(db)`, PRAGMAs (WAL, FK, cache, busy_timeout), migration v0→v1 full DDL from SSOT (17 tables, 4 triggers, all indexes), transactional via `schema_version`
  - `pkg/pika/migrate_test.go` — NEW: 4 tests (new DB, idempotency, pragmas, FTS5 MATCH smoke)
- **Breaking:** None (new package `pkg/pika/`)

### [2026-05-01] feat(pika): config.go unified config — wave 0b Phase 1

- **ТЗ:** ТЗ-v2-0b: config.go — Unified Config (pkg/config/)
- **PR:** #2 (merged)
- **Files:**
  - `pkg/config/config_pika.go` — NEW: 18 Pika v3 types
  - `pkg/config/config_pika_test.go` — NEW: tests
- **Breaking:** None (additive only)

### [2026-05-02] feat(pika): config.go struct patching + legacy cleanup — wave 0b Phase 2

- **ТЗ:** ТЗ-v2-0b-p2: config.go — Phase 2 (struct patching + legacy cleanup)
- **PR:** #5 (merged)
- **Breaking:** Config versions 0/1/2 no longer supported.

### [2026-05-02] feat(pika): post-merge cleanup — wave 0b Phase 3

- **ТЗ:** ТЗ-v2-0b-p3: config.go — Post-merge cleanup
- **PR:** #6 (merged)
- **Breaking:** LoadConfig() now returns error if memory_db_path is empty.

---

## Wave 1: CRUD Layer (botmemory + session store + registry)

### [2026-05-02] feat(pika): botmemory.go — CRUD layer for bot_memory.db — wave 1a

- **PR:** #7
- **Breaking:** None (new file, additive only)

### [2026-05-02] fix(pika): botmemory.go — 5 SQL bugs vs DDL — wave 1a-fix

- **PR:** #7 (updated)

### [2026-05-03] feat(pika): PikaSessionStore — wave 1b Phase 1+2

- **PR:** #9
- **Breaking:** `pkg/memory` package removed.

### [2026-05-03] test: skip 4 legacy tests — transitional (D-136)

- **PR:** #9, #12

### [2026-05-03] fix(pika): remove linux/arm + exotic archs from build-all — CI fix

- **PR:** #11, #13

### [2026-05-04] feat(pika): registry.go — Registry CRUD + AtomID generator — wave 1c

- **PR:** #14
- **Breaking:** None (new files, additive only)

---

## Wave 2: Runtime Components (TRAIL/META, envelope, context manager)

### [2026-05-04] feat(pika): trail_meta.go — TRAIL ring buffer + META metrics — wave 2a

- **PR:** #16

### [2026-05-04] feat(pika): PikaContextManager + delete Seahorse/legacy + cleanup pipeline — wave 2b

- **PR:** existing PR on `feat/v2-2b-context-manager` branch

### [2026-05-04] feat(pika): envelope.go — unified tool response envelope — wave 2c

- **Breaking:** None (new files, additive only)

### [2026-05-04] fix(pika): SystemPrompt bypass in pipeline_setup.go + cleanup

---

## Wave 3: Tools, Router, Archivist

### [2026-05-04] feat(pika): archivist.go — wave 3a

- **PR:** #21

### [2026-05-04] feat(pika): tool_router.go — wave 3b

- **PR:** #22

### [2026-05-04] feat(pika): memory_tools.go — wave 3c

- **PR:** #23

### [2026-05-04] feat(pika): clarify.go — wave 3d

- **PR:** #24

### [2026-05-04] feat(pika): autoevent.go — wave 3e

- **PR:** #25

---

## Wave 4: Pipeline Integration

### [2026-05-05] feat(pika): telemetry.go — wave 4f

- **PR:** #26

### [2026-05-05] feat(pika): session.go — wave 4b

- **PR:** #28

### [2026-05-05] feat(pika): toolguard.go — ToolGuard AfterLLM hook — wave 4c

- **ТЗ:** ТЗ-v2-4c: toolguard.go — ToolGuard (AfterLLM builtin hook)
- **PR:** #30
- **Files:**
  - `pkg/pika/toolguard.go` — NEW: `ToolGuard` struct with local hook types (HookAction, HookDecision, ToolGuardLLMResponse). `ActivePlanGetter` interface (implemented by PikaContextManager). `ToolGuardFactory(cfg, planGetter)` constructor. `AfterLLM(resp)` — detects missing tool call when ACTIVE_PLAN active: plan=="" → continue, HasToolCalls → continue, Content=="" → continue, retryCount≥max → continue (exhausted), otherwise → HookActionModify with reminder. `ResetTurn()` — per-turn retry counter reset. Max 1 retry. **No import of pkg/agent** (avoids import cycle via context_pika.go). Wiring adapter in instance.go (ТЗ-4a) converts local types ↔ agent.LLMInterceptor.
  - `pkg/pika/toolguard_test.go` — NEW: 8 tests (ActivePlanTextNoTools_Modify, ActivePlanWithToolCalls_Continue, NoPlan_Continue, RetryExhausted_Continue, EmptyResponse_Continue, NilPlanGetter_Continue, NilResponse_Continue, ResetTurn)
- **Breaking:** None (new files, additive only). Consumer: instance.go (ТЗ-4a) via wiring adapter
- **Dependencies:** `pkg/config` (ToolGuardFactory signature), `pkg/logger`

### [2026-05-05] feat(pika): confirm_gate.go — ConfirmGate ToolApprover hook — wave 4d

- **ТЗ:** ТЗ-v2-4d: confirm_gate.go — ConfirmGate (ToolApprover builtin hook)
- **PR:** #31
- **Files:**
  - `pkg/pika/confirm_gate.go` — NEW: `ConfirmGate` struct implementing ToolApprover (D-136a). Local types: `ConfirmApprovalRequest`, `ConfirmApprovalDecision` (mirror agent.ToolApprovalRequest/ApprovalDecision). `TelegramSender` interface (shared pattern with clarify.go ClarifySender). `ConfirmGateFactory(cfg, sender, health)` constructor. `ApproveTool(ctx, req)` — matches tool.operation against `security.dangerous_ops` config, evaluates confirm rules (always/if_healthy/if_critical_path/never), reflex for compose.restart+exited, sends Telegram confirmation, fail-closed on timeout/error. Helper functions: `getOperation`, `isExited`, `isInCriticalPath` (glob match), `extractPath`, `summarizeArgs`, `evaluateConfirmRule`. Uses existing `SystemStateProvider` from interfaces.go and `config.DangerousOpEntry`/`ConfirmMode` from config_pika.go. **No import of pkg/agent** (avoids import cycle).
  - `pkg/pika/confirm_gate_test.go` — NEW: 9 tests (DeployRequest_Approved, DeployRequest_Denied, ComposeRestart_Exited, ComposeRestart_Healthy, ComposeRestart_Degraded, FilesWrite_CriticalPath, FilesWrite_NonCritical, NotInTable, Timeout_Deny)
- **Breaking:** None (new files, additive only). Consumer: instance.go (ТЗ-4a) via wiring adapter
- **Dependencies:** `pkg/config` (SecurityConfig, DangerousOpsConfig, ConfirmMode), `pkg/logger`, `pkg/pika/interfaces.go` (SystemStateProvider)

---

## Wave 5: Sub-agents

### [2026-05-05] feat(pika): atomizer.go — Atomizer pipeline — wave 5a

- **ТЗ:** ТЗ-v2-5a: atomizer.go — Atomizer pipeline
- **PR:** #33 (merged)
- **Files:**
  - `pkg/pika/atomizer.go` — NEW: `Atomizer` struct — Go-pipeline extracting knowledge atoms from hot buffer. `AtomizerConfig` (D-133: trigger_tokens=800k, chunk_max_tokens=200k, prompt_file, max_retries=2, model=background). `DefaultAtomizerConfig()`. `AtomLLMOutput` / `atomizerLLMResponse` — LLM structured output types. `NewAtomizer(mem, atomGen, provider, telemetry, cfg)` constructor. `ShouldAtomize(ctx, sessionID)` — threshold check. `Run(ctx, sessionID)` — full pipeline: chunk selection (oldest turns ≤ budget) → hot-reload prompt (`os.ReadFile`) → LLM call (structured output, 0 tool calls) → parse+validate (category/polarity/confidence/source_turns) → retry loop (up to MaxRetries with REPAIR prompt on validation error) → INSERT atoms (via `AtomIDGenerator.Next` + `BotMemory.InsertAtom`) → archive+delete (1 txn via `BotMemory.ArchiveAndDeleteTurns`). Tags inherited from events per turn (D-75: `collectTagsByTurn` + `mergeTagsForTurns`). Telemetry: `ReportComponentFailure/ReportComponentSuccess`. Helper: `getMessagesByTurns` (same-package access to `BotMemory.db`). JSON extraction: `extractAtomizerJSON` + `extractBalanced`. Default prompt constant `defaultAtomizerPrompt`.
  - `pkg/pika/atomizer_test.go` — NEW: 16 tests
- **Breaking:** None (new files, additive only)

### [2026-05-05] feat(pika): reflector.go — Reflector pipeline — wave 5b

- **ТЗ:** ТЗ-v2-5b: reflector.go — Reflector pipeline
- **PR:** #34
- **Files:**
  - `pkg/pika/reflector.go` — NEW: `ReflectorPipeline` struct — Go-pipeline for behavioral optimization via cheap LLM (structured output, 0 tool calls). 3 modes (D-134): daily (1 day), weekly (7 days), monthly (full scan). 4 tasks: (1) Merge duplicates (D-147: polarity validation, 1 txn), (2) Pattern detection, (3) Confidence updates (D-59: clamp 0.0–1.0, no time decay F8-8), (4) Runbook drafts (D-87/F9-5). Monthly: crystallization + stale marking. Hot-reload prompt (D-90). Retry 1x on invalid JSON.
  - `pkg/pika/reflector_cron.go` — NEW: `RegisterReflectorJobs(cronSvc, pipeline, schedule)` — registers 3 cron jobs in upstream CronService. `HandleReflectorJob(pipeline, job)` — dispatches to pipeline.Run. `schedToCronExpr` — schedule string → cron expression conversion.
  - `pkg/pika/reflector_test.go` — NEW: 14+ tests (EmptyDB, ParseJSON, Validation, ConfidenceClamp, MergePolarityMismatch, MergeSuccess, RunbookDraft, DailyPipeline, PromptHotReload, CronExpr, RegisterJobs valid/empty/invalid, HandleJob)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go`, `pkg/pika/registry.go` (AtomIDGenerator), `pkg/pika/telemetry.go`, `pkg/providers`, `pkg/cron` (upstream as-is)

---

## Wave 6: Security

### [2026-05-05] feat(pika): rad.go — Reasoning Anomaly Detector — wave 6a

- **ТЗ:** ТЗ-v2-6a: rad.go — Reasoning Anomaly Detector
- **PR:** #35
- **Files:**
  - `pkg/pika/rad.go` — NEW: `RAD` struct — fast pre-action security gate on reasoning tokens (D-SEC-v2, Layer 6). 0 LLM, sync. Types: `RADVerdict` (safe/warning/anomaly), `RADResult` (verdict+score+detectors+reason), `RADConfig` (enabled, pattern keywords RU/EN, drift_threshold, block/warn scores), `RADSession` (minimal session view: last_tool_source, prev_keywords), `RADToolCall` (minimal pending call: name, risk_level). `DefaultRADConfig()` with production keywords. `NewRAD(cfg)` — compiles regex at creation (fail-fast on invalid patterns). `Analyze(ctx, reasoning, session, pendingCall)` — main entry point, runs 3 detectors: (1) Pattern Detector (+3): case-insensitive regex on configurable RU/EN keywords; (2) Drift Detector (+2): Jaccard keyword overlap < threshold after MCP call, skips non-MCP; (3) Escalation Detector (+2): red-risk action after MCP output. Scoring: ≥block_score(3)→ANOMALY, ≥warn_score(2)→WARNING, else SAFE. Helpers: `jaccardIndex`, `extractKeywords` (Unicode-aware tokenizer). autoEvent mapping: `rad.blocked`→`rad_anomaly`, `rad.warning`→`rad_warning` (critical class, defined in config toolTypeMap).
  - `pkg/pika/rad_test.go` — NEW: 15 tests (PatternDetect_RU, PatternDetect_EN, PatternDetect_CleanReasoning, DriftDetect_LowOverlap, DriftDetect_HighOverlap, DriftDetect_NonMCPSkip, EscalationDetect_RedAfterMCP, EscalationDetect_GreenAfterMCP, CompoundScoring_Safe, CompoundScoring_Warning, CompoundScoring_Anomaly, Disabled, JaccardIndex, ExtractKeywords, DriftPlusEscalation_Anomaly)
- **Breaking:** None (new files, additive only)
- **Dependencies:** None (standalone, 0 external imports from pkg/pika)

### [2026-05-06] feat(pika): mcp_security.go — MCP Security Pipeline — wave 6b

- **ТЗ:** ТЗ-v2-6b: mcp_security.go — MCP Security
- **PR:** #36
- **Files:**
  - `pkg/pika/mcp_security.go` — MODIFIED: rename extractJSON→extractGuardJSON (conflict with archivist.go)
  - `pkg/pika/mcp_security_test.go` — NEW: 24 tests covering all 15 acceptance criteria (Output Sanitizer, NFKC, credentials, taint tracking, ACL, capability negotiation, MCP Guard startup/canary, Rug Pull Guard, adaptive baseline, degraded mode, audit trail, prompt versioning)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/telemetry.go` (ReportComponentFailure/Success), `pkg/pika/autoevent.go` (EventClasses)

---

## Wave 7: Diagnostics

### [2026-05-06] feat(pika): diagnostics.go — Diagnostics Engine — wave 7a

- **ТЗ:** ТЗ-v2-7a
- **PR:** #37
- **Files:**
  - `pkg/pika/diagnostics.go` — NEW: `DiagnosticsEngine` struct — single point for subagent error diagnosis, correction rule (CR) management, and subagent prompt assembly with active CR injection. `Diagnose` (error attribution by trace_id, pattern detection ≥2 similar errors → SuggestedCR), `CreateCR` (insert CR into registry, TG notification D-149, threshold alert ≥3 active CRs), `BuildSubagentPrompt` (hot-reload base prompt + append active CRs within 500-token budget, oldest-trim), `IncrementVerified` (count++ on successful subagent call, auto-promote active→verified at threshold 5), `ReviewCRs` (weekly Reflector pipeline: promote verified+7d, deactivate active+30d+unverified). `CorrectionRule` type with lifecycle: active → verified → promoted/deactivated. Constants: `defaultMaxActiveCRs=10`, `defaultMaxCRTokens=500`, `defaultVerifyThreshold=5`, `defaultPromotionMinAgeDays=7`, `defaultDeactivationMaxAgeDays=30`. `validCRComponents` map for component validation. `estimateCRTokens` helper (~4 chars/token).
  - `pkg/pika/diagnostics_test.go` — NEW: 10 tests (`TestDiagnose_ErrorFound`, `TestDiagnose_NoErrors`, `TestDiagnose_SuggestedCR`, `TestCreateCR_Valid`, `TestCreateCR_InvalidComponent`, `TestBuildSubagentPrompt_NoCRs`, `TestBuildSubagentPrompt_WithCRs`, `TestBuildSubagentPrompt_TokenOverflow`, `TestBuildSubagentPrompt_MissingFile`, `TestIncrementVerified`, `TestReviewCRs`)
  - `pkg/pika/archivist.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `Archivist` struct, `loadPromptFile` now calls `BuildSubagentPrompt` with fallback to original behavior when diag=nil
  - `pkg/pika/atomizer.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `Atomizer` struct, same `loadPromptFile` fallback pattern
  - `pkg/pika/reflector.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `ReflectorPipeline` struct, same `loadPromptFile` fallback pattern (multi-line signature)
  - `pkg/pika/mcp_security.go` — MODIFIED: added `diag *DiagnosticsEngine` field to `MCPSecurityPipeline` struct, `loadGuardPrompt` now calls `BuildSubagentPrompt` with `cachedPromptSHA` update + fallback
- **Breaking:** None (new files, additive only; caller-side patches backward-compatible: diag=nil → original behavior)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory, registry table), `pkg/pika/interfaces.go` (TelegramSender), `pkg/pika/botmemory.go` (TraceSpanRow)

### [2026-05-06] feat(pika): analytics.go — Go-only Analytics Pipeline — wave 7b

- **ТЗ:** ТЗ-v2-7b
- **PR:** #38
- **Files:**
  - `pkg/config/config_pika_analytics.go` — NEW: `AnalyticsConfig` struct (schedule weekly/monthly cron, Telegram channels, anomaly thresholds), `AnalyticsSchedule` struct, `DefaultAnalyticsConfig()` with sensible defaults
  - `pkg/pika/analytics.go` — NEW: `AnalyticsEngine` struct — full Go-only analytics pipeline. `Run(ctx, mode)` orchestrates: period computation, metric collection (7 SQL query sets), delta calculation vs previous period, anomaly detection (7 rules: error rate, tool fail rate, latency P95, subagent errors, unused atoms, stale atoms, significant deltas), Telegram report formatting (≤4096 chars with auto-split), registry snapshot storage (kind=snapshot, upsert). Helper functions: `analyticsComputePeriods`, `analyticsComputeDeltas`, `analyticsDetectAnomalies`, `analyticsFormatReport`, `analyticsPercentile`, `analyticsSplitMessage`, `analyticsFormatCount`, `analyticsHasCritical`. Constants: `AnalyticsWeekly`/`AnalyticsMonthly`, 7 anomaly thresholds, `reportMaxTelegramChars=4096`
  - `pkg/pika/analytics_cron.go` — NEW: `RegisterAnalyticsJobs` (registers weekly+monthly cron jobs reusing `schedToCronExpr` from reflector), `HandleAnalyticsJob` (dispatches cron payload to engine.Run)
  - `pkg/pika/analytics_test.go` — NEW: 21 tests (CollectMetrics happy/partial/empty, Deltas increase/decrease/zero, Anomalies x7 + clean, FormatReport x2, StoreReport upsert, P95, SplitMessage, Periods weekly/monthly, HasCritical, FormatCount)
  - `workspace/queries/analytics_llm.sql` — NEW: LLM metrics (total requests, tokens, cost, avg/P95 latency, error rate, reasoning ratio, cost by component)
  - `workspace/queries/analytics_tools.sql` — NEW: Tool calling aggregates (requested/success/failed, success rate, top tools via json_each)
  - `workspace/queries/analytics_chains.sql` — NEW: Chain analysis (total chains, avg length, avg cost per chain)
  - `workspace/queries/analytics_subagents.sql` — NEW: Subagent health (error/timeout counts, avg/P95 duration per component)
  - `workspace/queries/analytics_knowledge.sql` — NEW: Knowledge quality (total atoms, new in period, by category/polarity/confidence bands)
  - `workspace/queries/analytics_atom_usage.sql` — NEW: Atom usage (total usages, unique atoms, effectiveness %, top atoms, unused count)
  - `workspace/queries/analytics_tasks.sql` — NEW: Task efficiency (top-5 tasks by cost, avg tokens/tools per task)
- **Breaking:** None (new files, additive only)
- **Dependencies:** `pkg/pika/botmemory.go` (BotMemory, registry table), `pkg/pika/interfaces.go` (TelegramSender), `pkg/config/config_pika_analytics.go` (AnalyticsConfig), `pkg/cron` (CronService, CronJob, CronSchedule)


### [2026-05-07] feat(pika): TZ-v2-8i — AutoEvent + RAD + Analytics wiring — wave 8i
- **T3:** TZ-v2-8i
- **Fixes:** #39
- **Files:**
  - `pkg/agent/hook_pika.go` — NEW: 'autoEventAdapter' struct wrapping 'pika.AutoEventHandler' as 'agent.EventObserver'. Translates 'EventKindToolExecEnd' → 'HandleToolResult'. Compile-time interface check added.
  - `pkg/agent/context_pika.go` — MOD: mount 'autoEventAdapter' as builtin hook via HookRegistration after BotMemory init. Set 'al.botmem = botmem' for RAD reasoning access.
  - `pkg/agent/agent.go` — MOD: added 'rad *pika.RAD' and 'botmem *pika.BotMemory' fields to AgentLoop. Added 'GetBotMemory()' public getter for gateway access.
  - `pkg/agent/agent_init.go` — MOD: RAD initialization from 'cfg.Security.RAD' after resolveContextManager(). Uses pika.NewRAD(pika.RADConfig{...}).
  - `pkg/agent/rad_gate.go` — NEW: 'radPreActionGate()' — direct RAD call in pipeline (NOT hook). Gets reasoning via BotMemory.GetLastReasoningText, calls RAD.Analyze, blocks on RADAnomaly, warns on RADWarning.
  - `pkg/agent/pipeline_execute.go` — MOD: inserted RAD pre-action gate before each tool call in ExecuteTools (D-136a checkpoint F16).
  - `pkg/pika/bus_sender.go` — NEW: 'BusSender' adapter (msgBus → TelegramSender interface). Universal sender for any connected messenger — not Telegram-specific.
  - `pkg/pika/analytics_cron.go` — NEW: 'AnalyticsCron' scheduler. Runs AnalyticsEngine.Run on weekly+monthly intervals via goroutines (D-136a checkpoint F17).
  - `pkg/gateway/gateway.go` — MOD: analytics wiring in restartServices() after CronService.Start(). Creates BusSender → AnalyticsEngine → AnalyticsCron pipeline.
  - `pkg/agent/rad_gate_test.go` — NEW: 3 tests (TestRadPreActionGate_NilRAD, TestRadPreActionGate_SafeTool, TestRadPreActionGate_WithBotmem)
  - `pkg/agent/hook_pika_test.go` — NEW: 2 tests (TestAutoEventAdapter_ImplementsEventObserver, TestAutoEventAdapter_NilHandler)
  - `pkg/pika/bus_sender_test.go` — NEW: 1 test (TestBusSender_ImplementsTelegramSender)
  - `pkg/pika/analytics_cron_test.go` — NEW: 3 tests (TestNewAnalyticsCron_Defaults, TestNewAnalyticsCron_CustomIntervals, TestAnalyticsCron_StartStop)
- **Breaking:** None (new files, additive only)
- **Dependencies:** pkg/pika/autoevent.go (wave 3e), pkg/pika/rad.go (wave 6a), pkg/pika/analytics.go (wave 7b), pkg/agent/hooks.go (upstream), pkg/bus/bus.go (upstream)
- **Design decisions:**
  - RAD: direct call in pipeline, NOT hook/EventObserver — per TZ-v2-8i spec. Reasoning extracted from BotMemory, not LLM response fields.
  - Analytics: BusSender wraps universal MessageBus instead of Telegram-specific channel. Bus routes to all connected messengers.
  - Analytics cron: goroutine-based (like HeartbeatService), not CronService jobs — simpler lifecycle, no cron expression parsing needed.

### [2026-05-10] feat(pika): ТЗ-v2-8j (Phase A) — Prompt files for subagents + MCP Guard fallback — wave 8
- **Files:**
  - `workspace/prompts/atomizer.md` — NEW: Atomizer system prompt extracted from defaultAtomizerPrompt Go constant. SSOT: Go code (pkg/pika/atomizer.go:642).
  - `workspace/prompts/archivist_build.md` — NEW: Archivist system prompt from Notion SSOT (Приложение: Промт Архивариуса v2). Version 2.2, unified search_context tool.
  - `workspace/prompts/reflexor.md` — NEW: Reflexor system prompt from Notion SSOT (Промт Рефлексора v1). XML-structured, 5 analysis sections, JSON output schema.
  - `workspace/prompts/mcp_guard.md` — NEW: MCP Guard system prompt from Notion SSOT (Приложение: Промт MCP Guard). English, 4-step CoT pipeline, STARTUP_AUDIT + RUNTIME_AUDIT modes.
  - `pkg/pika/mcp_security.go` — MOD: added `"errors"` import, `os.ErrNotExist` fallback in `LoadGuardPrompt()`, `defaultGuardPrompt` constant. Now matches D-90 fallback pattern used by archivist/atomizer/reflector.
- **Breaking:** None (new files, additive only; mcp_security.go fallback is backward-compatible)
- **Dependencies:** None (prompt files read at runtime via os.ReadFile, no go:embed)
- **Design decisions:**
  - All 4 subagent prompts stored as `workspace/prompts/*.md` — hot-reloadable at runtime via D-90 pattern (DiagnosticsEngine → file fallback → const fallback).
  - MCP Guard previously had no `defaultGuardPrompt` / `os.ErrNotExist` fallback — agent would crash if prompt file missing. Now aligned with other 3 subagents.
  - Backticks in mcp_guard.md replaced with single quotes in Go `defaultGuardPrompt` const (Go raw strings cannot contain backticks). File version preserves original formatting.
  - Prompt content sources: atomizer from Go code, archivist/reflexor/mcp_guard from Notion SSOT pages.

### [2026-05-10] feat(pika): memory pipeline — use MemoryDBPath from config — wave 8a
- **ТЗ:** ТЗ-v2-8j Phase Б
- **PR:** TBD
- **Files:**
  - `pkg/agent/instance.go` — MODIFIED:
    - `initSessionStore(dir string)` → `initSessionStore(dbPath string)`: принимает полный путь к DB вместо директории. Убран `filepath.Join(dir, "bot_memory.db")`, используется `filepath.Dir(dbPath)` для MkdirAll
    - Строки 120-123: хардкод `filepath.Join(workspace, "sessions")` заменён на `cfg.Agents.Defaults.MemoryDBPath`
    - NEW функция `migrateMemoryDB(workspace, newPath)`: при первом запуске переносит `sessions/bot_memory.db` → `memory/bot_memory.db` через `os.Rename`. No-op если target существует или legacy отсутствует
  - `workspace/memory/MEMORY.md` — DELETED: upstream шаблон для текстовой памяти, не используется Pika v3 (у нас SQL через bot_memory.db)
- **Breaking:** bot_memory.db перемещается из `sessions/` в `memory/` при первом запуске. Миграция автоматическая, данные не теряются
- **Rollback:** `git revert` коммита. После revert вручную `mv workspace/memory/bot_memory.db workspace/sessions/bot_memory.db`. Данные сохраняются — это тот же SQLite файл
- **Config:** `cfg.Agents.Defaults.MemoryDBPath` (default: `workspace/memory/bot_memory.db`, задаётся в `defaults.go:44`). Поле существовало ранее, но игнорировалось instance.go — теперь используется

### [2026-05-10] feat(pika): ТЗ-v2-8j (Phase В) — PromptContributor refactor + upstream bootstrap — wave 8
- **ТЗ:** ТЗ-v2-8j Phase В
- **PR:** TBD
- **Files:**
- `workspace/AGENT.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 SSOT content from Notion (CORE.md v4 §2.2b). Role DevOps, 3-question thinking, 8 NEVER rules with WHY, antipatterns, examples, plan markup. 96 lines.
- `workspace/SOUL.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 personality. Russian, trust boundaries, security invariants. 30 lines.
- `workspace/USER.md` — REWRITTEN: default PicoClaw template replaced with Pika v3 user context from Notion (CONTEXT.md §2.3). Manager garry, server paths, 5 work modes, risk matrix. 44 lines.
- `pkg/pika/context_manager.go` — MOD: added 5 exported getters (GetArchivist, GetStateProvider, GetPlanStore, ExtractActivePlan, BuildDegradationBlock) for PromptContributor access. BuildSystemPrompt() preserved but no longer called from Assemble.
- `pkg/agent/context_pika.go` — REWRITTEN: Assemble() returns empty SystemPrompt (pipeline falls to upstream else-branch). 4 new PromptContributor structs registered: pikaMemoryBriefContributor (pika:memory_brief), pikaTrailContributor (pika:trail), pikaActivePlanContributor (pika:active_plan), pikaDegradationContributor (pika:degradation). 379 lines.
- `pkg/agent/context.go` — MOD: getIdentity() patched — removed MEMORY.md and Daily Notes references from workspace description, removed rule 3 (Memory update instruction), reduced Sprintf args from 6 to 3. Prevents conflict with Archivist memory management.
- **Breaking:** System prompt assembly path changed from Pika if-branch to upstream else-branch. Prompt content now comes from AGENT.md/SOUL.md/USER.md (upstream LoadBootstrapFiles) + 4 PromptContributors instead of CORE.md/CONTEXT.md (which never existed as files).
- **Rollback:** `git revert` of commit. Restores old context_pika.go (BuildSystemPrompt path), old getIdentity() with MEMORY.md refs, old bootstrap file contents. Pika returns to if-branch (same behavior as before Phase В).
- **Dependencies:** pkg/agent/prompt.go (PromptContributor interface, PromptRegistry), pkg/pika/context_manager.go (Trail, Meta, Archivist, SystemStateProvider), pkg/agent/context.go (ContextBuilder.RegisterPromptContributor)
- **Design decisions:**
  - Upstream else-branch chosen over custom if-branch: one prompt assembly path instead of two. Upstream provides identity, bootstrap files, skills catalog, dynamic context, conversation summary. Pika adds 4 contributors via PromptRegistry.
  - MEMORY.md references removed from getIdentity(): prevents model from writing to MEMORY.md (conflicts with Archivist-managed bot_memory.db). GetMemoryContext() already returns empty (file deleted in Phase Б).
  - META removed from system prompt: was always non-empty (made BuildSystemPrompt never return ""). Channel payload delivery deferred to follow-up PR.
  - CORE.md/CONTEXT.md were never created as files — content was always in Notion SSOT. Now properly mapped: CORE.md content → AGENT.md, personality → SOUL.md, context → USER.md.
  - PlanStore updated inside pikaActivePlanContributor for wave 4 compatibility.

### [2026-05-10] refactor(pika): ТЗ-v2-8j cleanup — remove dead BuildSystemPrompt code — wave 8
- **ТЗ:** ТЗ-v2-8j (post Phase В cleanup)
- **Files:**
- `pkg/pika/context_manager.go` — MOD: BuildSystemPrompt() gutted to stub (return "", nil). Deleted: loadBootstrapFile(), getCached(), setCached(), InvalidateCache(). Removed struct fields: mu, cachedCore, cachedContext, coreModTime, contextModTime. Removed imports: os, filepath, time, sync. 215 lines (was 370).
- `pkg/pika/context_manager_test.go` — MOD: deleted 7 dead tests (TestBuildSystemPrompt_*, TestInvalidateCache). 66 lines (was 282). Surviving: TestCompact_NoOp, TestIngest_NoOp, TestClear_NoOp, TestAlwaysHealthyProvider, TestNoopArchivistCaller.
- **Breaking:** None (BuildSystemPrompt was already dead code — Assemble returns empty SystemPrompt since Phase В)
- **Design decisions:**
  - BuildSystemPrompt kept as stub (not deleted) for API compatibility — method signature preserved, body returns "", nil.
  - CORE.md/CONTEXT.md loading, file cache, InvalidateCache all removed — no longer needed since prompt content comes from upstream LoadBootstrapFiles (AGENT.md/SOUL.md/USER.md) + 4 PromptContributors.

### [2026-05-10] fix(deps): Go 1.25.10 — govulncheck green (ТЗ-v2-8q) — wave 8
- **ТЗ:** ТЗ-v2-8q
- **Files:**
- `go.mod` — MOD: `go 1.25.9` → `go 1.25.10`. Fixes 3 stdlib vulnerabilities: GO-2026-4976 (net/http/httputil), GO-2026-4971 (net), GO-2026-4918 (net/http).
- **Breaking:** None (patch-level stdlib upgrade only)

### [2026-05-10] fix(pika): ТЗ-v2-8l — Upstream embed fix + prompt protection — wave 8
- **ТЗ:** ТЗ-v2-8l
- **PR:** #41
- **Files:**
  - `cmd/picoclaw/internal/onboard/helpers.go` — MOD: `onboard()` signature: added `resetPrompts bool`. `createWorkspaceTemplates()` signature: added `preservePrompts bool`. `copyEmbeddedToTarget()` signature: added `preservePrompts bool`. Added skip logic: when `preservePrompts=true`, existing `prompts/*.md` files are not overwritten on re-onboard. Added `"strings"` import.
  - `cmd/picoclaw/internal/onboard/command.go` — MOD: added `--reset-prompts` CLI flag (default `false`). Passes `resetPrompts` to `onboard()`.
  - `pkg/config/config_pika.go` — MOD: added `OnboardConfig` struct with `PreserveUserPrompts bool`.
  - `pkg/config/config.go` — MOD: added `Onboard OnboardConfig` field to `Config` struct.
  - `pkg/config/defaults.go` — MOD: added `Onboard: OnboardConfig{PreserveUserPrompts: true}` default.
- **Breaking:** None (additive only, default behavior preserved for first onboard)
- **Design decisions:**
  - Prompt protection is config-driven (`onboard.preserve_user_prompts`, default `true`) — user controls via WebUI toggle or config.json.
  - CLI `--reset-prompts` flag is one-shot override: resets prompts in this run without changing config.
  - Only `prompts/*.md` are protected; other workspace files (SOUL.md, USER.md, skills/) update on re-onboard. Rationale: prompts are user-tunable via hot-reload, other files are upstream templates.
  - WebUI dashboard toggle deferred to separate follow-up (frontend change).

### [2026-05-10] feat(pika): ТЗ-v2-8l part 2c — WebUI toggle for prompt protection — wave 8
- **ТЗ:** ТЗ-v2-8l (часть 2c — WebUI)
- **PR:** #43
- **Files:**
  - `web/frontend/src/components/config/form-model.ts` — MODIFIED: added `preserveUserPrompts` field, default, and config parser
  - `web/frontend/src/components/config/config-sections.tsx` — MODIFIED: added `OnboardSection` with toggle
  - `web/frontend/src/components/config/config-page.tsx` — MODIFIED: import, render, and patchAppConfig mapping
- **Breaking:** None

---

## Wave 9: Wiring Audit

### [2026-05-12] feat(pika): ТЗ-v2-9b — Pipeline Wiring: Atomizer, Reflector, MCPSecurity, Diagnostics — wave 9b

- **ТЗ:** ТЗ-v2-9b: Pipeline Wiring
- **PR:** #46
- **Files:**
  - `pkg/agent/context_pika.go` — MOD: NewDiagnosticsEngine + NewAtomizer + NewReflectorPipeline + NewMCPSecurityPipeline creation. SetDiagnostics() calls to all subagents.
  - `pkg/agent/agent.go` — MOD: added reflector *pika.ReflectorPipeline + mcpSecurity *pika.MCPSecurityPipeline fields and GetReflector()/GetMCPSecurity() getters.
  - `pkg/agent/pipeline_finalize.go` — MOD: replaced no-op stub with Atomizer trigger (ShouldAtomize → Run in goroutine after each turn).
  - `pkg/agent/pipeline_execute.go` — MOD: MCPSecurity ProcessToolOutput call after MCP tool Execute. Simplified from switch/verdict to single facade call.
  - `pkg/gateway/gateway.go` — MOD: RegisterReflectorJobs + HandleReflectorJob in restart/reload path after analyticsCron.
  - `pkg/pika/archivist.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/atomizer.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/reflector.go` — MOD: added SetDiagnostics() setter.
  - `pkg/pika/mcp_security.go` — MOD: added SetDiagnostics() setter + ProcessToolOutput() facade (verdict logic inside pika, not agent).
  - `pkg/tools/integration/mcp_tool.go` — ROLLED BACK to main (audit fix: upstream file, ТЗ-v2-6b forbids modification).
- **Breaking:** None (all guards: if component != nil)
- **Known limitation:** RegisterReflectorJobs only in restart path (not cold start). Safe: nil-check skips. Works after first reload.
- **Dependencies:** pkg/pika/atomizer.go (wave 5a), pkg/pika/reflector.go (wave 5b), pkg/pika/mcp_security.go (wave 6b), pkg/pika/diagnostics.go (wave 7a)

### [2026-05-12] feat(config): extract hardcoded analytics/subagent settings into config — wave 8h
- **ТЗ:** ТЗ-v2-8h
- **PR:** #47
- **Files:**
  - `pkg/config/config_pika_analytics.go` — MODIFIED: extended `AnalyticsConfig` from 3 to 15 fields (Enabled, QueriesDir, Schedule, 7 thresholds: ToolFailThresholdPct/LLMErrorThresholdPct/LatencyP95ThresholdMs/UnusedAtomsPct/StaleAtomsPct/DeltaSpikePct/AnomalyWindowHours, 4 limits: TopQueriesLimit/TopAtomsLimit/ReportMaxLines/HistoryRetentionDays, DisableTelegramReports). `DefaultAnalyticsConfig()` with production defaults replacing 11 hardcoded consts from analytics.go.
  - `pkg/config/config.go` — MODIFIED: added `Analytics AnalyticsConfig` field to global `Config` struct (line 54).
  - `pkg/agent/config_mappers.go` — NEW: 5 config mappers (`mapAtomizerConfig`, `mapReflectorConfig`, `mapArchivistConfig`, `mapMCPGuardConfig`, `mapTelemetryConfig`) — replace hardcoded `Default*Config()` with values resolved from unified config via `cfg.ResolveAgentConfig()`.
  - `pkg/agent/context_pika.go` — MODIFIED: replaced 5× `Default*Config()` calls with `map*Config(cfg)` calls (Archivist :70, Atomizer :80, Reflector :88, MCPGuard :95, Telemetry :105).
  - `pkg/pika/analytics.go` — MODIFIED: removed 11 hardcoded const (thresholds/limits). Added `cfg` field to `AnalyticsEngine`. `NewAnalyticsEngine()` signature changed — accepts `config.AnalyticsConfig` as first arg. Added `applyAnalyticsDefaults()` for zero-value fallback. `analyticsDetectAnomalies()` accepts `config.AnalyticsConfig` as third arg. `DisableTelegramReports` flag wraps periodic report delivery (alerts always sent).
  - `pkg/pika/analytics_cron_service.go` — NEW: CronService-based analytics scheduler (prepared for Block 4 migration from ticker). Blocked by `SetOnJob` dispatcher ordering — deferred.
  - `pkg/gateway/gateway.go` — MODIFIED: analytics engine creation uses `cfg.Analytics` instead of zero-value. Reflector schedule read from `cfg.ResolveAgentConfig("reflexor").Schedule` with fallback defaults ("03:00"/"Sun 04:00"/"1st 05:00"). Removed broken `HandleAnalyticsJob` block from `SetOnJob` dispatcher (lines 815-823). Fixed `fmt.Println` placement inside `if botmem != nil` block.
  - `pkg/pika/analytics_test.go` — MODIFIED: updated 8× `analyticsDetectAnomalies()` calls + 4× `NewAnalyticsEngine()` calls for new signatures. Added `config` import. Zero-value `AnalyticsConfig{}` wrapped in `applyAnalyticsDefaults()` for correct threshold defaults in tests.
- **Breaking:** `NewAnalyticsEngine()` signature changed (config.AnalyticsConfig added as first arg). `analyticsDetectAnomalies()` signature changed (config.AnalyticsConfig added as third arg).
- **Dependencies:** `pkg/config/config_pika_analytics.go` (AnalyticsConfig), `pkg/config/config_pika.go` (ResolveAgentConfig, ResolvedAgentConfig, ScheduleConfig)
- **Design decisions:**
  - DisableTelegramReports controls only periodic reports. Alerts (anomalies, degraded) always go to manager chat — per founder requirement.
  - analytics_cron_service.go created but not wired — CronService migration blocked by SetOnJob dispatcher ordering (documented in ТЗ as Block 4).
  - Reflector schedule uses fallback defaults when config values are empty — backward-compatible with existing deployments without explicit schedule config.

### [2026-05-12] feat(pika): analytics CronService migration — ТЗ-v2-8h block 4 — wave 8
- **ТЗ:** ТЗ-v2-8h (Block 4)
- **PR:** #47
- **Files:**
  - `pkg/gateway/gateway.go` — MODIFIED: analytics engine created before setupCronTool (available in SetOnJob closure). Added `analyticsEngine *pika.AnalyticsEngine` parameter to setupCronTool. HandleAnalyticsJob added to SetOnJob dispatcher (analytics → reflector → fallback chain). RegisterAnalyticsJobs called after CronService.Start() in both setupAndStartServices and restartServices. Removed old ticker block (NewAnalyticsCron).
  - `pkg/pika/analytics_cron.go` — DELETED: custom ticker-based AnalyticsCron struct (Start/Stop/loop goroutines). Replaced by analytics_cron_service.go (CronService pattern).
  - `pkg/pika/analytics_cron_test.go` — DELETED: tests for removed ticker (TestNewAnalyticsCron_Defaults, TestNewAnalyticsCron_CustomIntervals, TestAnalyticsCron_StartStop).
- **Breaking:** None (analytics schedule unchanged, cron expressions generated from same config fields Schedule.Weekly/Monthly)
- **Dependencies:** pkg/pika/analytics_cron_service.go (wave 8i — already existed, now wired), pkg/cron (upstream CronService)
- **Design decisions:**
  - Analytics migrated from custom ticker goroutines to upstream CronService — same pattern as Reflector (RegisterJobs + HandleJob in SetOnJob dispatcher).
  - Engine created before setupCronTool so it's captured in SetOnJob closure — avoids needing engine in services struct.
  - analytics_cron_service.go reuses schedToCronExpr from reflector_cron.go — no duplicate parsing logic.
  - Fallback defaults "Sun 04:30" / "1st 05:30" when config schedule is empty — backward-compatible.

## Memory Pipeline Refactoring

### [2026-05-19] feat(pika): Рефакторинг memory pipeline — chat_id/session_id + Архивариус + SessionLifecycle
- **ТЗ:** ТЗ-v2-fix-memory-pipeline (7 фаз, 12 коммитов)
- **PR:** TBD
- **Files:**
  - `pkg/pika/migrate.go` — MOD: migrationV2 — rename session_id→chat_id, turn_id→pika_session_id (idempotent)
  - `pkg/pika/migrate_test.go` — MOD: адаптация под новые имена колонок
  - `pkg/pika/botmemory.go` — MOD: все SQL переведены на chat_id/pika_session_id, session-scoped queries
  - `pkg/pika/botmemory_session.go` — NEW: session-scoped helpers
  - `pkg/pika/botmemory_test.go` — MOD: адаптация тестов
  - `pkg/pika/session_store.go` — MOD: rewrite — turnIDs→SessionLifecycle, PikaSessionID int→string (pika_ prefix)
  - `pkg/pika/session_store_accessor.go` — MOD: string session ID adaptation
  - `pkg/pika/session_store_test.go` — MOD: тесты под новую логику
  - `pkg/pika/session.go` — MOD: wire SessionLifecycle — EnsureSession, Touch, CheckRotationTriggers
  - `pkg/pika/interfaces.go` — MOD: расширение интерфейсов (session/archivist types)
  - `pkg/pika/archivist.go` — MOD: ArchivistInput expanded (catalogs, tool_prefs, correlated_tools), mandatory blocking
  - `pkg/pika/discover_tools.go` — NEW: dynamic tool discovery из registry
  - `pkg/pika/memory_tools.go` — MOD: session-scoped GetHistory
  - `pkg/pika/atomizer.go` — MOD: session_id rename
  - `pkg/pika/reflector.go` — MOD: session_id rename
  - `pkg/pika/reflector_test.go` — MOD: тесты
  - `pkg/pika/diagnostics.go` — MOD: session_id rename
  - `pkg/pika/telemetry.go` — MOD: session_id rename
  - `pkg/pika/autoevent.go` — MOD: session_id rename в event keys
  - `pkg/pika/autoevent_test.go` — MOD: тесты
  - `pkg/pika/analytics_test.go` — MOD: session_id rename в fixtures
  - `pkg/agent/context_pika.go` — MOD: wire Archivist mandatory blocking + catalogs + recommended tools/skills
  - `pkg/agent/context.go` — MOD: удалены legacy memory references
  - `pkg/agent/memory.go` — DELETED: legacy MemoryStore (158 строк)
  - `pkg/agent/hook_pika.go` — MOD: session_id type adaptation
  - `pkg/agent/pipeline_llm.go` — MOD: tool defs filtering по recommended_tools
  - `pkg/agent/pipeline_setup.go` — MOD: session_id type adaptation
  - `pkg/agent/turn_coord.go` — MOD: session lifecycle touch point
  - `workspace/prompts/archivist_build.md` — MOD: расширенный промпт (catalogs, enrichment, JSON schema)
- **Breaking:** session_id→chat_id, turn_id→pika_session_id (migration included). PikaSessionID int→string. Legacy MemoryStore deleted
- **Rollback:** git revert 12 коммитов. ALTER TABLE RENAME COLUMN обратим. memory.go из git history
- **Dependencies:** archivist.go (3a), session.go (4b), session_store.go (1b), botmemory.go (1a)
- **Design decisions:**
  - session_id=TEXT с pika_ prefix — отличает новые сессии от legacy int
  - Архивариус = mandatory blocking, без fallback. Лучше подождать чем отвечать без памяти
  - Legacy MemoryStore удалён полностью — Archivist pipeline заменяет его
  - discover_tools.go создан но НЕ зарегистрирован в ToolRouter (wiring в отдельном ТЗ)
  - correlated_tools + tool_prefs enrichment в ТЗ Ф5 но ещё не в коде

## Tool Injector + Progressive Disclosure

### [2026-05-21] feat(pika): ТЗ-v2-3b-wiring — Tool Injector + Progressive Disclosure
- **ТЗ:** ТЗ-v2-3b-wiring
- **Branch:** feat/toolrouter-wiring
- **Commits:** 38e69196, 9f419abc, 3b351df6, 49dea032, dea0d493, 6ca55096, e5bd6137, 8c4d17f9
- **Files:**
  - `pkg/pika/tool_router.go` — DELETED: мёртвый dispatch (Route, RegisterBrain, RegisterBase, RegisterMCPTool). 310 строк.
  - `pkg/pika/tool_router_test.go` — DELETED: тесты для удалённого dispatch. 630 строк.
  - `pkg/pika/discover_tools.go` — MOD: переписан — правильная сигнатура Execute(ctx, args), работает с upstream ToolRegistry (GetSummaries + SnapshotHiddenTools). Зарегистрирован через Register(), IsCore=true.
  - `pkg/pika/memory_tools.go` — MOD: зарегистрирован в upstream ToolRegistry через Register(), IsCore=true.
  - `pkg/pika/clarify.go` — MOD: удалён мёртвый ClarifySender интерфейс. Добавлен ClarifyBus интерфейс (PublishOutbound + InboundChan) — удовлетворяется и *bus.MessageBus и interfaces.MessageBus. chatID/channel из ctx (toolshared.ToolChatID/ToolChannel) вместо хардкода в struct.
  - `pkg/pika/clarify_test.go` — MOD: удалён mockSender, тесты используют *bus.MessageBus + toolshared.WithToolInboundContext для ctx.
  - `pkg/pika/integration_smoke_test.go` — MOD: Step 5 обновлён (ToolRouter → DiscoverTools), smokeClarSender удалён.
  - `pkg/pika/archivist.go` — MOD: SearchContextResult расширен (CorrelatedTools, ToolPrefs). Два новых аспекта в executeSearchContext: correlated_tools (atom_usage JOIN), tool_prefs (category filter). Добавлены в default aspects.
  - `pkg/pika/botmemory.go` — MOD: добавлен CorrelatedToolRow struct + QueryCorrelatedTools() — SQL JOIN atom_usage × knowledge_atoms_fts.
  - `pkg/agent/instance.go` — MOD: добавлен msgBus pika.ClarifyBus параметр в NewAgentInstance. Блок регистрации clarify (guarded by msgBus != nil && cfg.Clarify.Enabled).
  - `pkg/agent/registry.go` — MOD: добавлен msgBus pika.ClarifyBus параметр в NewAgentRegistry, проброс в NewAgentInstance. Добавлен import pika.
  - `pkg/agent/agent.go` — MOD: NewAgentRegistry вызов с al.bus (interfaces.MessageBus удовлетворяет pika.ClarifyBus).
  - `pkg/agent/agent_init.go` — MOD: NewAgentRegistry вызов с msgBus (*bus.MessageBus).
  - `pkg/agent/context_pika.go` — MOD: добавлен вызов agent.Tools.PromoteTools(RecommendedTools, TTL=2) после BuildPrompt, guarded by cfg.ToolSelection.Enabled.
  - `pkg/agent/instance_test.go` — MOD: все NewAgentInstance вызовы с nil msgBus.
  - `pkg/agent/agent_test.go` — MOD: все NewAgentRegistry вызовы с nil msgBus.
  - `pkg/agent/registry_test.go` — MOD: все NewAgentRegistry вызовы с nil msgBus.
  - `pkg/config/config_pika.go` — MOD: добавлен ToolSelectionConfig struct (Enabled, MaxRecommendedTools, MaxRecommendedSkills).
  - `pkg/config/config.go` — MOD: добавлено поле ToolSelection ToolSelectionConfig.
  - `pkg/config/defaults.go` — MOD: дефолты ToolSelection (enabled=false, max_tools=8, max_skills=3).
  - `workspace/prompts/archivist_build.md` — MOD: документация correlated_tools + tool_prefs в fan-out выходе, инструкции по использованию.
- **Breaking:** NewAgentRegistry и NewAgentInstance — новый параметр msgBus (nil-safe). NewAgentRegistry import pika добавлен.
- **Rollback:** git revert 8 коммитов. ToolRouter восстановить из git history. Убрать ClarifyBus, вернуть *bus.MessageBus. Убрать ToolSelectionConfig из config.
- **Dependencies:** upstream ToolRegistry (Register, PromoteTools, ToProviderDefs), upstream toolshared (ToolChatID, ToolChannel, WithToolInboundContext), bus.MessageBus
- **Design decisions:**
  - ToolRouter полностью удалён — dispatch дублировал upstream ExecuteWithContext(). -940 строк мёртвого кода.
  - ClarifyBus — минимальный интерфейс в pika (2 метода), избегает import cycle agent/interfaces → pika. Удовлетворяется обоими типами.
  - chatID не хардкодится — берётся из ctx на каждый вызов Execute(). Upstream уже кладёт через WithToolInboundContext.
  - ToolSelection.Enabled=false по умолчанию — Progressive Disclosure выключен, безопасно мержить. Включение через конфиг.
  - PromoteTools TTL=2 — рекомендованные тулы живут текущий turn + 1 запасной.
  - correlated_tools: статистический сигнал из atom_usage (какие тулы вызывались после похожих атомов).
  - tool_prefs: пользовательские предпочтения из knowledge_atoms с category=tool_pref.

---

## Wave 14: Env Wiring

### [2026-08-07] feat(pika): wire PIKA_CONFIG/PIKA_DB_PATH, drop dead PIKA_BINARY/PIKA_BUILTIN_SKILLS — wave 14

- **ТЗ:** ТЗ-v2-14: Проводка PIKA_CONFIG и PIKA_DB_PATH, удаление PIKA_BINARY и PIKA_BUILTIN_SKILLS
- **PR:** #59
- **Files:**
  - `pkg/config/envkeys.go` — MOD: удалены мёртвые константы `EnvPikaBinary`, `EnvPikaBuiltinSkills`; комментарии `EnvPikaConfig`/`EnvPikaDBPath` обновлены под приоритет
  - `cmd/picoclaw/internal/helpers.go` — MOD: `GetConfigPath()` — `PIKA_CONFIG` приоритетнее `PICOCLAW_CONFIG`
  - `web/backend/utils/runtime.go` — MOD: `GetDefaultConfigPath()` — тот же приоритет в лаунчере
  - `pkg/agent/instance.go` — MOD: `PIKA_DB_PATH` перекрывает `agents.defaults.memory_db_path` в `NewAgentInstance`
  - `cmd/picoclaw/internal/helpers_test.go` — MOD: новый тест `TestGetConfigPath_WithPIKA_CONFIG`
  - `docs/guides/configuration.md` — MOD: таблица env — добавлены `PIKA_HOME`, `PIKA_CONFIG`, `PIKA_DB_PATH`
  - `.gitignore` — MOD: игнор `index.scip` (артефакт анализаторов)
- **Breaking:** None (upstream-переменные работают как раньше; PIKA_* только с приоритетом)
- **Decisions:** D-AUDIT-47 (доказательство мёртвости: SCIP-граф + GitHub-поиск + grep), D-AUDIT-48 (решение wire/drop)

## Wave 15: Pika builtin hooks wiring (Р-1)

### [2026-08-08] feat(pika): register Pika builtin hooks via RegisterBuiltinHook — wave 15
- **Задача:** Р-1 · **Решение:** D-AUDIT-49
- **PR:** pending
- **Files:**
  - `pkg/agent/hook_pika.go` — MODIFIED: registerPikaBuiltinHooks (4 фабрики: pika.output_gate, pika.toolguard, pika.confirm_gate, pika.progress) + nil-guard в progressAdapter.OnEvent
  - `pkg/agent/agent_init.go` — MODIFIED: вызов registerPikaBuiltinHooks в NewAgentLoop после resolveContextManager
  - `pkg/agent/hook_pika_mount_test.go` — NEW: 4 теста (флаги включают/выключают монтирование, глобальный off, двойная регистрация)
  - `config/config.example.json` — MODIFIED: секция hooks.builtins с 4 флагами (output_gate/toolguard on, confirm_gate/progress off до Р-3)
- **Breaking:** None — confirm_gate инертен при пустой dangerous_ops, progress монтируется как no-op до появления отправителя (Р-3)

## Wave 16: Diagnostics/MCP wiring — manager sender + prompt paths + policies (Р-3)

### [2026-08-08] feat(pika): wire manager sender, prompt paths, MCP policies — wave 16
- **Задача:** Р-3 · **Решение:** D-AUDIT-50
- **PR:** pending
- **Files:**
  - `pkg/config/config_pika.go` — MOD: HealthReportingConfig += `manager_channel`, `manager_chat_id` (пусто = отчёты отключены)
  - `pkg/pika/bus_sender.go` — MOD: BusSender.MB расширен до интерфейса OutboundPublisher (покрывает *bus.MessageBus и interfaces.MessageBus); NEW конструктор NewManagerSender (nil, если адрес не настроен)
  - `pkg/pika/manager_sender_test.go` — NEW: 2 теста NewManagerSender
  - `pkg/agent/config_mappers.go` — MOD: NEW mapMCPServerPolicies (политика на каждый включённый сервер из tools.mcp.servers × дефолты security.mcp; deny-by-default) + NEW pikaPromptPaths (4 промт-файла)
  - `pkg/agent/config_mappers_r3_test.go` — NEW: 3 теста (политики + покрытие validCRComponents)
  - `pkg/agent/context_pika.go` — MOD: NewDiagnosticsEngine получает живого отправителя + promptPaths (инъекция correction rules оживает во всех 4 субагентах); NewMCPSecurityPipeline получает политики вместо nil
  - `pkg/agent/hook_pika.go` — MOD: фабрика pika.progress — живой ProgressObserver при настроенном адресе, иначе no-op (как в PR #61)
  - `pkg/gateway/gateway.go` — MOD: отправитель аналитики — менеджерский адрес в 2 местах (cold start + restart); без адреса — поведение как раньше
  - `config/config.example.json` — MOD: секция health.reporting с manager_channel/manager_chat_id
- **Breaking:** None — без manager_channel/manager_chat_id всё работает как раньше; потребитель MCP-политик в проде пока отсутствует (оживёт в Р-5, deny-by-default)
- **Verified without code change:** Reflector.SetDiagnostics(al.diag) уже вызывался в context_pika.go — пункт 3 задачи Р-3 закрыт фактом

## Wave 17: Security config block (Р-5 step 1)

### [2026-08-08] feat(config): real security block in example config — wave 17
- **Задача:** Р-5 шаг 1 · **Решения:** D-AUDIT-51, D-AUDIT-52
- **PR:** #63
- **Files:**
  - `config/config.example.json` — MOD: секция security (dangerous_ops с классами эффектов по D-AUDIT-52, critical_paths, rad, mcp — значения = проверенные дефолты)
- **Breaking:** None (только пример конфига)

## Wave 18: Confirm gate by effect + live SendConfirmation (Р-5 step 2)

### [2026-08-08] feat(pika): confirm gate by effect + live SendConfirmation — wave 18
- **Задача:** Р-5 шаг 2 · **Решения:** D-AUDIT-51, D-AUDIT-52
- **PR:** pending
- **Files:**
  - `pkg/pika/confirm_gate.go` — MOD: гейт по эффекту. deriveEffects: exec-команда режется на сегменты (; && || | &, кавычки, env-префиксы, флаги со значениями), классификация compose/systemctl/git/curl/scp/редиректов; файловые инструменты → files.write. Вооружённый гейт (непустая таблица) + нераспознанный эффект → спрашиваем (deny-by-default к операциям). if_healthy + деградация → allow + уведомление менеджеру. Legacy tool.operation и рефлекс exited сохранены.
  - `pkg/pika/bus_sender.go` — MOD: живой SendConfirmation (вопрос менеджеру → ожидание да/нет из его чата с таймаутом, паттерн clarify.go). OutboundPublisher += InboundChan.
  - `pkg/pika/bus_sender_test.go` — MOD: старый тест под живую семантику (таймаут → отказ)
  - `pkg/pika/confirm_gate_effect_test.go` — NEW: 14 тестов детектора + 4 теста диалога на реальной шине
  - `pkg/agent/hook_pika.go` — MOD: фабрика confirm_gate получает NewManagerSender (health.reporting.manager_*)
- **Breaking:** None по умолчанию: гейт разоружён при пустой ops-таблице, флаг pika.confirm_gate в примере выключен, без адреса менеджера — fail-closed. Активация = таблица + адрес + флаг.

## Wave 19: Critical paths hardening (Р-5 step 3)

### [2026-08-08] feat(pika): harden critical path matching — Clean + ** — wave 19
- **Задача:** Р-5 шаг 3
- **PR:** pending
- **Files:**
  - `pkg/pika/confirm_gate.go` — MOD: isInCriticalPath переписан — filepath.Clean (обходы через ./ и ../ закрыты), рекурсивный ** (любое число сегментов), относительные пути проверяются и с ведущим /. Одиночная * по-прежнему не пересекает разделители — старые шаблоны работают как раньше.
  - `pkg/pika/confirm_gate_paths_test.go` — NEW: 4 теста (критерий 2 Р-5: пути с точками; **; одиночная *; относительные пути)
- **Breaking:** None (шаблоны без ** сохраняют семантику filepath.Match)

## Wave 20: Per-server ACL в схему MCP (Р-5 step 5)

### [2026-08-08] feat(config+pika): per-server ACL for MCP servers — wave 20
- **Задача:** Р-5 шаг 5 (финальный)
- **PR:** pending
- **Files:**
  - `pkg/config/config_pika.go` — MOD: MCPSecurityConfig += `servers` (map[string]MCPServerACLConfig); NEW тип MCPServerACLConfig (trust_level, allowed_tools, allow_prompts/allow_resources как *bool для отличия «не задано» от false, max_output_bytes, taint_policy, rpm)
  - `pkg/agent/config_mappers.go` — MOD: mapMCPServerPolicies читает per-server ACL; пустые поля наследуют дефолты security.mcp
  - `config/config.example.json` — MOD: пример security.mcp.servers
  - `pkg/config/config_pika_test.go` — MOD: TestMCPServerACLConfig_JSON (парсинг, *bool-семантика)
  - `pkg/agent/config_mappers_r3_test.go` — MOD: TestMapMCPServerPolicies_PerServerACL (переопределения и наследование)
- **Breaking:** None — пустой servers = поведение Р-3 (deny-by-default с дефолтами). Потребитель политик в проде появится отдельно; пока схема + маппер + тесты.

## Wave 21: RAD keywords wiring (Р-2)

### [2026-08-08] feat(pika): wire RAD pattern keywords to detector — wave 21
- **Задача:** Р-2 · **Решение:** D-AUDIT-53
- **PR:** pending
- **Files:**
  - `pkg/config/config_pika.go` — MOD: RADConfig += `pattern_keywords_ru`, `pattern_keywords_en` ([]string; пусто = дефолтные списки)
  - `pkg/agent/config_mappers.go` — MOD: NEW mapRADConfig — база pika.DefaultRADConfig() (16 фраз), переопределения из конфига
  - `pkg/agent/agent_init.go` — MOD: NewRAD получает mapRADConfig(cfg.Security.RAD) — фразы доезжают до детектора
  - `config/config.example.json` — MOD: фразы видимы в security.rad
  - `pkg/agent/config_mappers_r3_test.go` — MOD: 2 теста mapRADConfig (дефолты + переопределения)
  - `pkg/pika/rad_config_test.go` — NEW: критерий 1 (фраза → 3 балла) + критерий 3 (ядовитые фразы без паники — QuoteMeta)
- **Breaking:** None (пустые поля = дефолтные фразы; warn-режим на первый прогон — через security.rad.block_score: 4 в боевом конфиге, D-AUDIT-53)
- **Verified:** deadcode больше не показывает rad.go:55 (DefaultRADConfig достижим)

## Wave 22: Steering/SubTurn dead code cleanup (Р-4 branch 1)

### [2026-08-08] chore(agent): remove legacy steering API + wire SubTurn result helper — wave 22
- **Задача:** Р-4 (группа B) · **Решение:** D-AUDIT-54
- **PR:** pending
- **Files:**
  - `pkg/agent/steering.go` — MOD: удалены легаси-обёртки `push`/`dequeue`/`len` + `dequeueSteeringMessages` (upstream-остатки до-scope API; живые scoped-варианты делают то же)
  - `pkg/agent/subturn.go` — MOD: удалена публичная API-пара `SpawnSubTurn` + `AgentLoopFromContext` (прод идёт через SetSpawner-замыкание в agent_init.go; sync subturn.md → задача Р-7)
  - `pkg/agent/turn_coord.go` — MOD: inline-поллинг pendingResults заменён на общий хелпер `dequeuePendingSubTurnResults` (одна точка правды, subturn.md); хелпер сливает ВСЕ готовые результаты за итерацию (было: один)
  - `pkg/agent/steering_test.go`, `subturn_test.go` — MOD: тесты переписаны на scoped API (`pushScope`/`dequeueScope`/`lenScope`), покрытие сохранено
- **Происхождение (проверено по ТЗ):** обе группы — upstream PicoClaw (sync до v0.2.6); D-136 явно отказался от зависимости на них
- **Breaking:** None для рантайма; публичный API пакета agent сужен (SpawnSubTurn/AgentLoopFromContext удалены) — внутренних вызывающих не было
- **Verified:** deadcode по steering/subturn пуст (было 7 функций); тесты pkg/agent зелёные

## Wave 23: Context budget dead code cleanup (Р-4 branch 2)

### [2026-08-08] chore(agent): remove turn-boundary helpers (upstream #1316) — wave 23
- **Задача:** Р-4 (группа C) · **Решение:** D-AUDIT-54
- **PR:** pending
- **Files:**
  - `pkg/agent/context_budget.go` — MOD: удалены parseTurnBoundaries / isSafeBoundary / findSafeBoundary (upstream-код под компрессию #1316; потребитель снят нашей волной 2b Phase C; D-136 явно отказался от зависимости). Живое (EstimateMessageTokens/EstimateToolDefsTokens/isOverContextBudget) не тронуто
  - `pkg/agent/context_budget_test.go` — MOD: удалены 7 тестов удалённых функций + осиротевший комментарий; знание сохранено в git-истории, вернёмся в волне 4 (сессионная ротация)
- **Breaking:** None
- **Verified:** deadcode по context_budget пуст; в pkg/agent (без подпакета adapters) осталось 8 мёртвых функций (группа D + 2 хвоста группы A) — критерий ≤15 выполнен

## Wave 24: Adapters package + hook helpers cleanup

### [2026-08-08] chore(agent): delete dead adapters package + legacy hook helpers — wave 24
- **Решение:** D-AUDIT-55 (OK founder'а 8 авг 2026, после глубокого разбора)
- **PR:** pending
- **Files:**
  - `pkg/agent/adapters/channelmanager.go` + `messagebus.go` — DEL: пакет целиком (13 функций, чистая пересылка вызовов, 0 импортов; обёртки над интерфейсами запрещены манифестом D-136). НЕ интеграции каналов — те в pkg/channels/ и не тронуты
  - `pkg/agent/hooks.go` — MOD: удалён NamedHook (после Р-1 регистрации собирает loadConfiguredHooks)
  - `pkg/agent/hook_mount.go` — MOD: удалён unregisterBuiltinHook (тестовая гигиена, не нужна с sync.Once из Р-1)
  - `pkg/agent/hooks_test.go` — MOD: 17 вызовов NamedHook → явный HookRegistration (1-в-1)
  - `pkg/agent/agent_test.go` — MOD: 1 вызов NamedHook → HookRegistration
  - `pkg/agent/hook_mount_test.go` — MOD: убран t.Cleanup с unregisterBuiltinHook (имя хука уникально)
  - `docs/architecture/hooks/README.md` + `README.zh.md` — MOD: пример монтирования обновлён под HookRegistration
  - `pkg/pika/toolguard.go` — MOD: устаревший комментарий про NamedHook → фактическая регистрация (Р-1)
- **Breaking:** None для рантайма; публичный API пакета agent сужен (NamedHook удалён) — боевых вызывающих не было
- **Verified:** deadcode по pkg/agent: 8 → 6 (только группа D, вне scope)

## Wave 25: Group D dead code cleanup — pkg/agent deadcode zero

### [2026-08-08] chore(agent): delete last 6 dead functions — pkg/agent deadcode zero — wave 25
- **Решение:** D-AUDIT-56 (OK founder'а 8 авг 2026 после глубокого разбора и подтверждения upstream-происхождения)
- **PR:** pending
- **Files:**
  - `pkg/agent/agent_mcp.go` — MOD: удалён `mcpRuntime.hasManager` (никем не звался; соседи getManager/takeManager живы)
  - `pkg/agent/agent_utils.go` — MOD: удалены `toolFeedbackExplanationFromResponse`/`FromToolCalls` (вытеснены живой `toolFeedbackExplanationForToolCall`) и `isNativeSearchProvider`/`filterClientWebSearch` (дубликат-заготовка; фича native search жива через PreferNative + useNativeSearch в pipeline_llm.go, покрыта TestPipeline_CallLLM_UsesNativeSearch…)
  - `pkg/agent/turn_state.go` — MOD: удалён экспортированный `TurnStateFromContext` (инструменты идут через Spawner-интерфейс)
  - `pkg/agent/agent_test.go` — MOD: вырезаны 6 тестов мёртвых функций + 2 использования hasManager переписаны на getManager(); восстановлен тип overflowProvider (побочка вырезателя, урок записан)
  - `pkg/agent/agent_mcp_test.go` — MOD: 3 использования hasManager → getManager()
- **Breaking:** None для рантайма
- **Verified:** deadcode по pkg/agent = 0 (было 25 на снимке e7587d4); сборка/vet/тесты зелёные
