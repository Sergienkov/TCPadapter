# TCPadapter

TCPadapter — сервис на Go для работы с TCP-контроллерами по бинарному протоколу с очередями команд, TTL, retry и восстановлением состояния после рестарта.

Текущий репозиторий содержит рабочий baseline без реальной Kafka-интеграции (вместо Kafka сейчас используется `LoggingBus`).

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
  - retry с backoff
  - завершение по лимиту попыток
- Персистентность:
  - file-based store (`.data/sessions.json` по умолчанию)
  - восстановление очередей/in-flight/seq после рестарта
- Защита очередей от переполнения:
  - лимит по глубине (`max_depth`)
  - лимит по байтам (`max_bytes`, оценочный)
  - политика переполнения: `reject` / `drop_oldest`
- Наблюдаемость:
  - `/healthz`, `/readyz`, `/metrics`
  - `/debug/queues` (только при `TCPADAPTER_DEBUG=true`)

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
make smoke
```

По умолчанию:

- `ADAPTER_ADDR=:15010`
- `METRICS_ADDR=:18080`
- `SIM_ADDR=127.0.0.1:15010`
- `SIM_CLIENTS=10`

## Конфигурация (env)

- `TCPADAPTER_LISTEN_ADDR` (default `:15010`) — TCP порт адаптера.
- `TCPADAPTER_METRICS_ADDR` (default `:18080`) — HTTP порт observability.
- `TCPADAPTER_REG_WINDOW` (default `5s`) — окно ожидания регистрации.
- `TCPADAPTER_HEARTBEAT_TIMEOUT` (default `30s`) — heartbeat timeout.
- `TCPADAPTER_READ_TIMEOUT` (default `35s`) — read deadline соединения.
- `TCPADAPTER_WRITE_TIMEOUT` (default `10s`) — write deadline.
- `TCPADAPTER_MAX_CONNECTIONS` (default `10000`) — лимит TCP-соединений.
- `TCPADAPTER_QUEUE_MAX_DEPTH` (default `1000`) — лимит сообщений в очереди контроллера.
- `TCPADAPTER_QUEUE_MAX_BYTES` (default `1048576`) — лимит оценочного размера очереди в байтах.
- `TCPADAPTER_QUEUE_OVERFLOW` (default `drop_oldest`) — `drop_oldest` или `reject`.
- `TCPADAPTER_ACK_TIMEOUT` (default `10s`) — ожидание ack до retry.
- `TCPADAPTER_RETRY_BACKOFF` (default `3s`) — дополнительная задержка между retry.
- `TCPADAPTER_MAX_RETRIES` (default `3`) — максимум повторных отправок.
- `TCPADAPTER_SWEEP_INTERVAL` (default `2s`) — интервал фонового sweeper.
- `TCPADAPTER_STATE_FILE` (default `.data/sessions.json`) — файл состояния.
- `TCPADAPTER_DEBUG` (default `false`) — включает debug-функции (`/debug/queues`, `/debug/enqueue`).

## HTTP endpoints

- `GET /healthz` -> `200 OK`
- `GET /readyz` -> `200 OK`
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
    "message_id":"manual-1"
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
- `tcpadapter_queue_overflow_total{limit="...",policy="...",action="..."}`
- `tcpadapter_queue_depth_sum`, `tcpadapter_queue_depth_max`
- `tcpadapter_queue_bytes_sum`, `tcpadapter_queue_bytes_max`
- `tcpadapter_inflight_sum`, `tcpadapter_inflight_max`

## Структура проекта

- `cmd/tcpadapter` — точка входа.
- `cmd/controller-sim` — эмулятор нескольких TCP-контроллеров.
- `internal/config` — загрузка/валидация конфигурации.
- `internal/server` — TCP сервер, sweeper, HTTP observability.
- `internal/session` — сессии контроллеров, очереди, in-flight, overflow.
- `internal/queue` — приоритетная очередь и TTL.
- `internal/protocol` — binary codec и парсеры payload.
- `internal/store` — in-memory/file store.
- `internal/kafka` — текущая заглушка шины.

## Документы

- `TECH_SPEC.md` — структурированное ТЗ по проекту.
- `Протокол.csv` — исходная спецификация бинарного протокола.

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
