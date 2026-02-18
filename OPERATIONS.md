# TCPadapter Operations Runbook

Этот документ описывает минимальный набор SLI/SLO и практические алерты для текущего состояния проекта (без Kafka).

## 1. Базовые SLI

- Доступность адаптера:
  - `GET /healthz` должен отвечать `200`.
  - `GET /readyz` должен отвечать `200` вне окна старта/shutdown.
- Состояние доставки:
  - `tcpadapter_ack_total{status="delivered|failed|expired|retrying|accepted"}`
  - `tcpadapter_ack_latency_seconds` (histogram).
- Нагрузка очередей:
  - `tcpadapter_queue_depth_max`
  - `tcpadapter_queue_bytes_max`
  - `tcpadapter_inflight_max`
- Сетевые риски и защита:
  - `tcpadapter_ip_rejected_total{reason="per_ip_limit|blocked"}`
  - `tcpadapter_ip_violations_total`
- Корректность shutdown:
  - `tcpadapter_shutdown_drain_timeouts_total`
  - `tcpadapter_shutdown_drain_last_seconds`

## 2. Рекомендуемые алерты

Ниже пример для окна `5m` (подстройте под вашу систему алертинга):

1. `HighFailedAckRate`
- Условие: `failed / terminal > 5%` в `5m`.
- Метрики:
  - `failed = increase(tcpadapter_ack_total{status="failed"}[5m])`
  - `terminal = increase(tcpadapter_ack_total{status=~"delivered|failed|expired|unsupported|duplicate"}[5m])`
- Действие: проверить рост `invalid_payload`, `execution_error`, `crc_error` в `tcpadapter_ack_terminal_total`.

2. `HighExpiredAckRate`
- Условие: `expired / terminal > 2%` в `5m`.
- Действие: проверить `ack timeout/retry` конфиг, сетевую деградацию и `inflight_max`.

3. `QueuePressure`
- Условие: `tcpadapter_queue_depth_max > 80%` от `TCPADAPTER_QUEUE_MAX_DEPTH` более `3m`.
- Действие: проверить горячие контроллеры через `/debug/queues`, увеличить лимиты или снизить входной поток.

4. `IPProtectionTriggered`
- Условие: `increase(tcpadapter_ip_rejected_total[5m]) > 0` (warning), `> 100` (critical).
- Действие: проверить источник IP, легитимность трафика, при необходимости скорректировать:
  - `TCPADAPTER_MAX_CONNECTIONS_PER_IP`
  - `TCPADAPTER_IP_VIOLATION_LIMIT`
  - `TCPADAPTER_IP_BLOCK_DURATION`

5. `ShutdownDrainTimeout`
- Условие: `increase(tcpadapter_shutdown_drain_timeouts_total[15m]) > 0`.
- Действие: анализ активных сессий во время остановки, проверить сетевые таймауты и корректность shutdown orchestration.

6. `ReadinessDrop`
- Условие: `readyz != 200` вне planned rollout окна.
- Действие: проверить логи старта/остановки, наличие bind-ошибок и внутренние аварии accept loop.

## 3. Быстрая диагностика

1. Проверка жизненности:
```bash
curl -fsS http://127.0.0.1:18080/healthz
curl -fsS -i http://127.0.0.1:18080/readyz
```

2. Срез метрик:
```bash
curl -fsS http://127.0.0.1:18080/metrics | rg 'tcpadapter_(ack|queue|ip|shutdown|inflight)'
```

3. Горячие очереди:
```bash
curl -fsS "http://127.0.0.1:18080/debug/queues?limit=20"
```

4. Локальный стресс-прогон:
```bash
make stress-run
cat artifacts/stress_report_*.txt
```

## 4. Triage по симптомам

1. Рост `failed` + `invalid_payload`:
- вероятно upstream формирует payload не по формату команды;
- проверить валидаторы `internal/protocol/server_payload_validation.go`.

2. Рост `expired` + `retrying`:
- деградация канала/контроллеров или слишком агрессивный retry policy;
- увеличить `TCPADAPTER_ACK_TIMEOUT*`, уменьшить нагрузку, проверить `Read/WriteTimeout`.

3. Рост `ip_rejected_total{reason="blocked"}`:
- возможен шум/атака или несоответствие регистрационного потока;
- временно смягчить лимиты, но собрать forensic по источникам IP.

4. `shutdown_drain_timeouts_total` растет:
- соединения не завершаются вовремя;
- проверить поведение клиентов при закрытии сокета и текущие `ReadTimeout`.

## 5. Operational Checklist (перед выкладкой)

1. `go test ./...` проходит.
2. `make stress-run` на staging проходит без аномального роста `failed/expired`.
3. Настроены алерты из раздела 2.
4. Подготовлен rollback plan (предыдущий стабильный бинарь + конфиг).
