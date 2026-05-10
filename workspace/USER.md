# User

## Пользователь
- **Имя:** garry
- **Telegram ID:** (заполнить при деплое)
- **Язык:** русский
- **Предпочтения:** краткость, один вариант с обоснованием, не спрашивать очевидное

## Decision tree маршрутизации задач

Задача от менеджера:
  автоматика (инфра, скрипты, данные) -> Пика-LLM выполняет сама
  готовый скрипт из registry + cron       -> Executor
  анализ/проектирование/документы         -> GAR (Notion AI)

## Сервер
- **ОС:** Ubuntu 22.04 (DigitalOcean Droplet)
- **Пути:**
  - /workspace/ — корень рабочего окружения
  - /workspace/memory/ — bot_memory.db
  - /workspace/scripts/ — скрипты
  - /workspace/skills/ — drop-in plugin пакеты
  - /workspace/prompts/ — промт-файлы субагентов
- **Notion DB IDs:** -> integrations/notion/config.json

## Режимы работы

| Режим | Когда | Разрешено | Заблокировано |
|-------|-------|-----------|---------------|
| routine | Крон, healthcheck, мониторинг. Дефолт | status, logs, sandbox(ro), snapshot, search_memory | restart, deploy, write, git push |
| fix | Сервис exited/unhealthy или alert | restart, inspect, logs --tail | deploy, git push, prompt write |
| deploy | Менеджер: задеплой/обнови | git.*, deploy.request, compose.up/down, files.write | prompt write |
| admin | Менеджер: admin или эскалация | Все, но каждый красный = confirm | -- |
| data | Скрипт, запрос к БД, обработка данных | sandbox.run, files.read/write scripts/, SELECT LIMIT | prompt write, compose.*, deploy.*, git push |

## Матрица рисков

| Цвет | Протокол | Примеры |
|------|----------|---------|
| зелёный | Выполняй сразу, логируй | status, logs, sandbox(ro), snapshot, search_memory, registry_write |
| жёлтый | PREFLIGHT - выполняй - VERIFY | restart, files.write(existing), git.commit, sandbox(write) |
| красный | confirm - PREFLIGHT - выполняй - VERIFY | deploy.*, compose.down, git.push, prompt.write, DELETE |

Не в таблице? -> считай красный. Спроси менеджера.
