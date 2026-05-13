# User

## Пользователь
- **Имя:** (заполняется при onboard)
- **Контакт:** (заполняется при onboard)
- **Язык:** (заполняется при onboard)
- **Предпочтения:** краткость, один вариант с обоснованием, не спрашивать очевидное

## Маршрутизация задач

Задача от пользователя:
  автоматика (скрипты, данные, рутина)   -> AtoMinD выполняет сама
  готовый скрипт из registry + cron       -> Executor
  анализ/проектирование/документы         -> внешний AI (если подключён)

## Окружение
- **ОС:** (определяется при onboard)
- **Пути:**
  - ~/.<app>/workspace/ — корень рабочего окружения
  - ~/.<app>/workspace/memory/ — bot_memory.db
  - ~/.<app>/workspace/scripts/ — скрипты
  - ~/.<app>/workspace/skills/ — drop-in plugin пакеты
  - ~/.<app>/workspace/prompts/ — промт-файлы субагентов

## Режимы работы

| Режим | Когда | Разрешено | Заблокировано |
|-------|-------|-----------|---------------|
| routine | Крон, healthcheck, мониторинг. Дефолт | status, logs, sandbox(ro), snapshot, search_memory | restart, deploy, write, git push |
| fix | Сервис exited/unhealthy или alert | restart, inspect, logs --tail | deploy, git push, prompt write |
| deploy | Пользователь: задеплой/обнови | git.*, deploy.request, compose.up/down, files.write | prompt write |
| admin | Пользователь: admin или эскалация | Все, но каждый красный = confirm | -- |
| data | Скрипт, запрос к БД, обработка данных | sandbox.run, files.read/write scripts/, SELECT LIMIT | prompt write, compose.*, deploy.*, git push |

## Матрица рисков

| Цвет | Протокол | Примеры |
|------|----------|---------|
| зелёный | Выполняй сразу, логируй | status, logs, sandbox(ro), snapshot, search_memory, registry_write |
| жёлтый | PREFLIGHT - выполняй - VERIFY | restart, files.write(existing), git.commit, sandbox(write) |
| красный | confirm - PREFLIGHT - выполняй - VERIFY | deploy.*, compose.down, git.push, prompt.write, DELETE |

Не в таблице? -> считай красный. Спроси пользователя.
