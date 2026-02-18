# TCPadapter

TCPadapter — сервис на Go для работы с TCP-контроллерами по бинарному протоколу с очередями команд, TTL, retry и восстановлением состояния после рестарта.

Текущий репозиторий содержит рабочий baseline без реальной Kafka-интеграции (вместо Kafka сейчас используется `LoggingBus`).

## Требования и статус

- Definition of Done: `DOD.md`
- Чеклист по требованиям: `REQUIREMENTS_CHECKLIST.md`
- Источники требований: `TECH_SPEC.md`, `Протокол.csv`

## Что уже реализовано

- TCP-сервер для подключений контроллеров.
- Регистрация контроллера первым пакетом (`cmd=1`).
- Базовый бинарный frame codec:
  - marker `SLDN` (4 байта)
  - длина (2 байта)
  - `ttl` (1 байт)
  - `seq` (1 байт)
  - payload
  - `CRC16/MODBUS` (2 байта)
- Парсеры ключевых сообщений:
  - регистрация (`cmd=1`)
  - status1 (`cmd=2`)
  - ack от контроллера (`cmd=11`)
  - статус FW (`cmd=4`)
- Индивидуальная очередь на каждый контроллер:
  - приоритеты `high | fw | sync | query`
  - offline enqueue (очередь создается даже без активного TCP)
  - attach очереди при reconnect
- TTL-обработка:
  - удаление просроченных сообщений перед отправкой
  - статус `expired` в ack-событиях
- Надежность доставки:
  - `in-flight` трекинг по `seq`
  - timeout ожидания ack
  - retry с экспоненциальным backoff и jitter
  - завершение по лимиту попыток
- Персистентность:
  - file-based store (`.data/sessions.json` по умолчанию)
  - атомарная запись state-файла (`temp + fsync + rename`)
  - при повреждении state JSON файл переносится в `.corrupt-*`, сервис стартует с пустым состоянием
  - хранится не более 5 corrupt-backup файлов (старые удаляются автоматически)
  - восстановление очередей/in-flight/seq после рестарта
- Защита очередей от переполнения:
  - лимит по глубине (`max_depth`)
  - лимит по байтам (`max_bytes`, оценочный)
  - политика переполнения: `reject` / `drop_oldest`
- Наблюдаемость:
  - `/healthz`, `/readyz`, `/metrics`
  - `/debug/queues` (только при `TCPADAPTER_DEBUG=true`)
  - маскирование `controller_id` в логах (в debug-режиме можно видеть полный ID)
- Корректный graceful shutdown:
  - остановка accept loop
  - закрытие активных TCP-соединений
  - ожидание drain обработчиков соединений

## Что пока не реализовано

- Реальный Kafka producer/consumer (только заглушка `LoggingBus`).
- Полный набор payload-кодеков для всех команд протокола.
- DLQ и полноценный production pipeline событий.

## Запуск

### Требования

- Go `1.22+` (проверено на `1.26.0`)

### Установка зависимостей и запуск

```bash
go test ./...
go run ./cmd/tcpadapter
```

### Сборка

```bash
go build ./cmd/tcpadapter
```

### Быстрые команды через Makefile

```bash
make test
make build
make run-adapter
make run-sim
make stress
make stress-run
make smoke
make docker-build
make docker-up
make docker-up-with-sim
make docker-down
```

По умолчанию:

- `ADAPTER_ADDR=:15010`
- `METRICS_ADDR=:18080`
- `SIM_ADDR=127.0.0.1:15010`
- `SIM_CLIENTS=10`

`make stress-run` запускает адаптер+симулятор, выдерживает нагрузку `DURATION_SEC` и сохраняет:
- `artifacts/stress_metrics_<timestamp>.prom`
- `artifacts/stress_report_<timestamp>.txt`

В `stress_report` дополнительно считаются KPI:
- `kpi_delivery_rate_pct`
- `kpi_fail_rate_pct`
- `kpi_expired_rate_pct`
- `kpi_retry_ratio_pct`
- `kpi_accepted_to_delivered_pct`

Чтобы во время stress-run автоматически генерировались серверные команды (и ACK KPI были не нулевые), включайте enqueue load:

```bash
ENQUEUE_LOAD=true ENQUEUE_QPS=20 DURATION_SEC=60 SIM_CLIENTS=100 make stress-run
```

Если порты заняты, задайте альтернативные:

```bash
ADAPTER_ADDR=:15110 METRICS_ADDR=:18180 SIM_ADDR=127.0.0.1:15110 make stress-run
```

Если параллельно крутятся другие симуляторы и возникают конфликты IMEI (`controller session already exists`), задайте отдельный диапазон:

```bash
SIM_BASE_IMEI=861000000000000 make stress-run
```

## Docker

### Быстрый старт (только адаптер)

```bash
make docker-build
make docker-up
make docker-ps
```

`docker-compose` поднимает `postgres + adapter`, адаптер по умолчанию использует `TCPADAPTER_STORE_BACKEND=postgres`.

Проверка:

```bash
curl -fsS http://127.0.0.1:18080/healthz
curl -fsS http://127.0.0.1:18080/readyz
curl -fsS http://127.0.0.1:18080/metrics >/dev/null
```

### Адаптер + симулятор контроллеров

```bash
SIM_CLIENTS=100 make docker-up-with-sim
```

Логи:

```bash
make docker-logs
docker compose logs -f --tail=200 controller-sim
```

Остановка:

```bash
make docker-down
```

## Конфигурация (env)

- `TCPADAPTER_LISTEN_ADDR` (default `:15010`) — TCP порт адаптера.
- `TCPADAPTER_METRICS_ADDR` (default `:18080`) — HTTP порт observability.
- `TCPADAPTER_REG_WINDOW` (default `5s`) — окно ожидания регистрации.
- `TCPADAPTER_HEARTBEAT_TIMEOUT` (default `30s`) — heartbeat timeout.
- `TCPADAPTER_READ_TIMEOUT` (default `35s`) — read deadline соединения.
- `TCPADAPTER_WRITE_TIMEOUT` (default `10s`) — write deadline.
- `TCPADAPTER_MAX_CONNECTIONS` (default `10000`) — лимит TCP-соединений.
- `TCPADAPTER_MAX_CONNECTIONS_PER_IP` (default `0`) — лимит TCP-соединений с одного IP (`0` = без лимита).
- `TCPADAPTER_IP_VIOLATION_WINDOW` (default `30s`) — окно подсчета нарушений протокола по IP.
- `TCPADAPTER_IP_VIOLATION_LIMIT` (default `10`) — сколько нарушений допускается в окне (`0` = выключено).
- `TCPADAPTER_IP_BLOCK_DURATION` (default `1m`) — длительность временной блокировки IP после превышения лимита.
- `TCPADAPTER_QUEUE_MAX_DEPTH` (default `1000`) — лимит сообщений в очереди контроллера.
- `TCPADAPTER_QUEUE_MAX_BYTES` (default `1048576`) — лимит оценочного размера очереди в байтах.
- `TCPADAPTER_QUEUE_OVERFLOW` (default `drop_oldest`) — `drop_oldest` или `reject`.
- `TCPADAPTER_ACK_TIMEOUT` (default `10s`) — ожидание ack до retry.
- `TCPADAPTER_RETRY_BACKOFF` (default `3s`) — дополнительная задержка между retry.
- `TCPADAPTER_MAX_RETRIES` (default `3`) — максимум повторных отправок.
- `TCPADAPTER_ACK_TIMEOUT_QUERY` (default `2s`) — профиль query-команд.
- `TCPADAPTER_RETRY_BACKOFF_QUERY` (default `1s`) — профиль query-команд.
- `TCPADAPTER_MAX_RETRIES_QUERY` (default `2`) — профиль query-команд.
- `TCPADAPTER_ACK_TIMEOUT_SYNC` (default `15s`) — профиль sync-команд.
- `TCPADAPTER_RETRY_BACKOFF_SYNC` (default `5s`) — профиль sync-команд.
- `TCPADAPTER_MAX_RETRIES_SYNC` (default `1`) — профиль sync-команд.
- `TCPADAPTER_ACK_TIMEOUT_FW` (default `20s`) — профиль FW-команд.
- `TCPADAPTER_RETRY_BACKOFF_FW` (default `3s`) — профиль FW-команд.
- `TCPADAPTER_MAX_RETRIES_FW` (default `3`) — профиль FW-команд.
- `TCPADAPTER_SWEEP_INTERVAL` (default `2s`) — интервал фонового sweeper.
- `TCPADAPTER_STORE_BACKEND` (default `file`) — backend хранения: `memory|file|postgres`.
- `TCPADAPTER_STATE_FILE` (default `.data/sessions.json`) — файл состояния для `file` backend.
- `TCPADAPTER_POSTGRES_DSN` (default empty) — DSN для `postgres` backend.
- `TCPADAPTER_DEBUG` (default `false`) — включает debug-функции (`/debug/queues`, `/debug/enqueue`).

## HTTP endpoints

- `GET /healthz` -> `200 OK`
- `GET /readyz` -> `200 OK` только когда сервер готов принимать трафик; `503` во время старта/дренажа shutdown
- `GET /metrics` -> Prometheus text format
- `GET /debug/queues?limit=20` -> JSON с топом контроллеров по нагрузке очереди (только если `TCPADAPTER_DEBUG=true`)
- `POST /debug/enqueue` -> ручная постановка команды в очередь контроллера (только если `TCPADAPTER_DEBUG=true`)

Пример ответа `/debug/queues`:

```json
{
  "count": 2,
  "items": [
    {
      "controller_id": "123456789012345",
      "queue_depth": 14,
      "queue_bytes": 1280,
      "in_flight": 2,
      "online": true,
      "last_seen": "2026-02-16T19:40:00Z"
    }
  ]
}
```

Пример запроса `/debug/enqueue`:

```bash
curl -X POST http://127.0.0.1:18080/debug/enqueue \
  -H 'Content-Type: application/json' \
  -d '{
    "controller_id":"860000000000001",
    "command_id":9,
    "ttl_seconds":10,
    "message_id":"manual-1",
    "trace_id":"trace-manual-1"
  }'
```

Пример команды с payload в hex (например `cmd=8`):

```bash
curl -X POST http://127.0.0.1:18080/debug/enqueue \
  -H 'Content-Type: application/json' \
  -d '{
    "controller_id":"860000000000001",
    "command_id":8,
    "payload_hex":"010100"
  }'
```

## Основные метрики

- `tcpadapter_active_connections`
- `tcpadapter_sessions_total`
- `tcpadapter_online_sessions_total`
- `tcpadapter_ack_total{status="..."}`
- `tcpadapter_ack_terminal_total{status="...",reason="..."}`
- `tcpadapter_ack_latency_seconds` (histogram)
- `tcpadapter_telemetry_total{command_id="...",trace_source="rx|command"}`
- `tcpadapter_queue_overflow_total{limit="...",policy="...",action="..."}`
- `tcpadapter_ip_violations_total`
- `tcpadapter_ip_rejected_total{reason="per_ip_limit|blocked"}`
- `tcpadapter_ip_blocks_applied_total`
- `tcpadapter_shutdown_drain_seconds` (summary)
- `tcpadapter_shutdown_drain_last_seconds`
- `tcpadapter_shutdown_drain_timeouts_total`
- `tcpadapter_queue_depth_sum`, `tcpadapter_queue_depth_max`
- `tcpadapter_queue_bytes_sum`, `tcpadapter_queue_bytes_max`
- `tcpadapter_inflight_sum`, `tcpadapter_inflight_max`

## ACK11 Семантика

Коды `ack11` от контроллера интерпретируются так:

- `0` -> `delivered` (terminal)
- `1` -> `failed` (`parameter_error`, terminal)
- `2` -> `failed` (`execution_error`, terminal)
- `3` -> `failed` (`invalid_result`, terminal)
- `4` -> `failed` (`password_required`, terminal)
- `5` -> `in_progress` (non-terminal, in-flight сохраняется)
- `6` -> `expired` (`stale_command`, terminal)
- `254` -> `failed` (`crc_error`, terminal)
- `255` -> `unsupported` (`command_not_supported`, terminal)
- неизвестный код -> `failed` (`unknown_code`, terminal)

Terminal-коды снимают команду из `in-flight`.  
`in_progress` не снимает команду и ожидается последующий terminal ack.

ACK-события содержат `trace_id` (если задан при enqueue; для debug enqueue генерируется автоматически при отсутствии).

## Структура проекта

- `cmd/tcpadapter` — точка входа.
- `cmd/controller-sim` — эмулятор нескольких TCP-контроллеров.
- `internal/config` — загрузка/валидация конфигурации.
- `internal/server` — TCP сервер, sweeper, HTTP observability.
- `internal/session` — сессии контроллеров, очереди, in-flight, overflow.
- `internal/queue` — приоритетная очередь и TTL.
- `internal/protocol` — binary codec и парсеры payload.
- `internal/store` — in-memory/file/postgres store.
- `internal/kafka` — текущая заглушка шины.

Для входящих пакетов контроллера (telemetry) адаптер проставляет `trace_id`.  
Если пакет относится к in-flight команде (например, `ack11`), используется trace исходной команды; иначе генерируется `rx-...`.

## Документы

- `TECH_SPEC.md` — структурированное ТЗ по проекту.
- `Протокол.csv` — исходная спецификация бинарного протокола.
- `OPERATIONS.md` — runbook, SLI/алерты и triage для эксплуатации.

## Ближайший roadmap

1. Подключить реальный Kafka клиент и контракты топиков.
2. Довести полный набор протокольных команд и строгую валидацию payload.
3. Добавить интеграционные тесты TCP<->broker и нагрузочные тесты на целевую емкость.

## Ручной e2e тест без Kafka

1. Запустить адаптер:

```bash
TCPADAPTER_DEBUG=true go run ./cmd/tcpadapter
```

2. Запустить эмулятор контроллеров:

```bash
go run ./cmd/controller-sim --addr 127.0.0.1:15010 --clients 10 --status-interval 3s
```

Нагрузочные режимы эмулятора:

- `--ack-delay=200ms` — задержка перед `ack11`.
- `--ack-error-rate=0.2` — 20% ack с кодом ошибки.
- `--ack-drop-rate=0.1` — 10% команд без ack.
- `--status-burst-size=5` — 5 status-пакетов за тик.
- `--status-burst-spacing=20ms` — интервал между пакетами в burst.

Пример стресс-режима:

```bash
go run ./cmd/controller-sim \
  --addr 127.0.0.1:15010 \
  --clients 50 \
  --status-interval 2s \
  --ack-delay 300ms \
  --ack-error-rate 0.15 \
  --ack-drop-rate 0.1 \
  --status-burst-size 4 \
  --status-burst-spacing 25ms
```

3. Подать команду в очередь через debug API:

```bash
curl -X POST http://127.0.0.1:18080/debug/enqueue \
  -H 'Content-Type: application/json' \
  -d '{"controller_id":"860000000000000","command_id":9}'
```

4. Проверить метрики и состояние очередей:

```bash
curl http://127.0.0.1:18080/metrics
curl 'http://127.0.0.1:18080/debug/queues?limit=20'
```

## Test Playbook

Ниже рекомендуемый порядок ручной/полуавтоматической проверки без Kafka.

1. Базовая живость

```bash
make run-adapter
curl -f http://127.0.0.1:18080/healthz
curl -f http://127.0.0.1:18080/readyz
```

2. Подключения контроллеров и heartbeat

```bash
make run-sim
curl http://127.0.0.1:18080/metrics | rg 'active_connections|online_sessions_total|sessions_total'
```

Ожидаемо:
- `tcpadapter_active_connections > 0`
- `tcpadapter_online_sessions_total > 0`

3. Ручная инъекция команды

```bash
make smoke
```

Ожидаемо:
- команда появляется в очереди (`/debug/queues`)
- в метриках растёт `tcpadapter_ack_total{status="accepted"}`

4. Retry/TTL сценарии

```bash
go run ./cmd/controller-sim --addr 127.0.0.1:15010 --clients 10 --ack-drop-rate 0.3
```

Ожидаемо:
- растут `retrying`, затем `delivered` или `expired` в `tcpadapter_ack_total`
- `tcpadapter_inflight_sum` не зависает на постоянном росте

5. Overflow сценарии

Запускать с малыми лимитами (`TCPADAPTER_QUEUE_MAX_DEPTH=1`) и подавать команды через `/debug/enqueue`.

Ожидаемо:
- `reject`: растёт `tcpadapter_queue_overflow_total{...,action="rejected"}`
- `drop_oldest`: растёт `...{...,action="dropped"}`

6. Восстановление после рестарта

Запускать с `TCPADAPTER_STATE_FILE` на диске, enqueue оффлайн-команды, перезапускать адаптер.

Ожидаемо:
- после рестарта очереди восстанавливаются
- при reconnect контроллера команды доходят до `delivered`
