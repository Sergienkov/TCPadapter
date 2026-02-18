# Definition of Done (DoD) — TCPadapter

## 1) Обязательный минимум для приемки релиза

Все пункты ниже должны быть выполнены одновременно.

- [ ] Код компилируется и тесты проходят: `go test ./...`
- [ ] Контейнеризация рабочая: `make docker-build`, `make docker-up`, `/readyz=200`
- [ ] Логика регистрации/heartbeat работает на реальном TCP-трафике
- [ ] Очереди/TTL/retry/in-flight работают и подтверждены e2e-тестами
- [ ] Персистентность и восстановление состояния после рестарта подтверждены
- [ ] Наблюдаемость (`/healthz`, `/readyz`, `/metrics`) и debug endpoints доступны
- [ ] Стресс по соединениям подтвержден минимум на `1000` активных TCP-соединений
- [ ] Нет критических багов класса data-loss/crash/deadlock в актуальном backlog

## 2) Протокольная готовность (MVP)

- [ ] Frame codec корректен: marker/len/ttl/seq/payload/crc16
- [ ] Регистрация контроллера (`cmd=1`) + ответ сервера (`cmd=1`) реализованы
- [ ] Обработка `status1 (cmd=2)` реализована
- [ ] Обработка `ack11 (cmd=11)` с terminal/non-terminal семантикой реализована
- [ ] Обработка `fw status (cmd=4)` реализована
- [ ] Валидация server payload для поддержанных команд включена

## 3) Надежность доставки

- [ ] Per-controller очередь поддерживается оффлайн/онлайн
- [ ] TTL-истечение формирует `expired`
- [ ] Retry/backoff и лимит попыток формируют `retrying/failed`
- [ ] ACK11 коррелируется с in-flight стабильно под нагрузкой
- [ ] Не более одной in-flight команды на контроллер (текущее проектное ограничение)

## 4) Персистентность и отказоустойчивость

- [ ] `TCPADAPTER_STATE_FILE` используется, запись атомарна
- [ ] При повреждении state-файла сервис стартует, corrupt-файл архивируется
- [ ] После рестарта pending/in-flight/seq восстанавливаются
- [ ] Graceful shutdown дренирует соединения и метрики отражают shutdown

## 5) Производительность (рабочие пороги)

### 5.1 Capacity (без командной нагрузки)
- [ ] `SIM_CLIENTS=1000`, `ENQUEUE_LOAD=false`:
  - `active_connections >= 1000`
  - `sessions_total >= 1000`
  - `ip_rejected_* = 0` (если не включены лимиты)

### 5.2 Command-load baseline (релевантный сценарий)
- [ ] Профиль:
  - `SIM_CLIENTS=100`
  - `ENQUEUE_LOAD=true`
  - `ENQUEUE_QPS=5..10`
  - `ENQUEUE_TTL_SECONDS=60`
  - `DURATION_SEC=60`
  - `WARMUP_SEC>=20`, `SETTLE_SEC>=12`, `SETTLE_MAX_SEC>=60`
- [ ] Целевые KPI:
  - `kpi_expired_rate_pct <= 10`
  - `kpi_fail_rate_pct <= 20`
  - `kpi_accepted_to_delivered_pct >= 60`

## 6) Операционная готовность

- [ ] Runbook (инцидентные шаги, health checks, stop/start) зафиксирован
- [ ] Метрики и лог-ключи достаточны для RCA:
  - `ack_total`, `ack_terminal_total`, `ack_latency`
  - `queue_depth_*`, `inflight_*`
  - `ip_*`
- [ ] Документация запуска локально и в Docker актуальна

## 7) Что не считается Done (пока)

Ниже отдельный этап, не блокирует текущий MVP только если явно согласовано.

- [ ] Полный Kafka pipeline (`command.in`, `ack.out`, `telemetry.out`, `event.out`, `dlq`)
- [ ] Полное покрытие всех команд из `Протокол.csv` в production-уровне
- [ ] Формальная поддержка нескольких версий протокола как runtime policy
