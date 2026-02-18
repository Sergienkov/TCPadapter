# Requirements Checklist — TCPadapter

Формат:
- `Status`: `done | partial | todo`
- `Evidence`: ссылка на файл/тест/метрику
- `How to verify`: команда проверки

## A. Сеть и сессии

1. TCP listener и прием соединений контроллеров  
Status: `done`  
Evidence: `internal/server/server.go`  
How to verify: `go test ./internal/server -run TestE2E`

2. Обязательная регистрация в окне `REG_WINDOW`  
Status: `done`  
Evidence: `internal/server/server.go`  
How to verify: `go test ./internal/server -run TestE2ERegistrationAckSent`

3. Ответ сервера на регистрацию (`cmd=1`)  
Status: `done`  
Evidence: `internal/server/server.go`, `internal/server/e2e_test.go`  
How to verify: `go test ./internal/server -run TestE2ERegistrationAckSent -v`

4. Heartbeat timeout  
Status: `done`  
Evidence: `internal/server/server.go`, `internal/server/e2e_test.go`  
How to verify: `go test ./internal/server -run TestE2EHeartbeatTimeoutDisconnectsWithoutStatus1 -v`

5. Лимит соединений и per-IP ограничения  
Status: `done`  
Evidence: `internal/server/server.go`, метрики `tcpadapter_ip_rejected_total`  
How to verify: нагрузка + `/metrics`

## B. Протокол и парсинг

6. Базовый frame codec (`SLDN`, length, ttl, seq, payload, crc16)  
Status: `done`  
Evidence: `internal/protocol/frame.go`, `internal/protocol/frame_test.go`  
How to verify: `go test ./internal/protocol`

7. Парсинг ключевых входящих сообщений (`1`, `2`, `11`, `4`)  
Status: `done`  
Evidence: `internal/protocol/messages.go`, `internal/server/server.go`  
How to verify: `go test ./internal/protocol ./internal/server`

8. Валидация payload server->controller  
Status: `partial`  
Evidence: `internal/protocol/server_payload_validation.go`  
How to verify: `go test ./internal/protocol -run TestValidate`
Notes: часть команд покрыта, но полный production-coverage по `Протокол.csv` еще не закрыт.

## C. Очереди и доставка

9. Индивидуальная очередь на контроллер (offline/online)  
Status: `done`  
Evidence: `internal/session/manager.go`, `internal/queue/queue.go`  
How to verify: `go test ./internal/session -run TestEnqueueForOfflineControllerCreatesQueue -v`

10. Приоритеты (`high|fw|sync|query`)  
Status: `done`  
Evidence: `internal/server/command_policy.go`, `internal/queue/queue.go`  
How to verify: `go test ./internal/server -run Test.*CommandPolicy -v`

11. TTL и удаление просроченных  
Status: `done`  
Evidence: `internal/server/server.go`, `internal/session/manager.go`  
How to verify: `go test ./internal/server -run TestE2EExpireWhenNoAck -v`

12. Retry/backoff/max_retries  
Status: `done`  
Evidence: `internal/session/manager.go`, `internal/session/manager_test.go`  
How to verify: `go test ./internal/session -run TestInFlight -v`

13. ACK11 -> in-flight корреляция  
Status: `partial`  
Evidence: `internal/server/server.go`, `internal/session/manager.go`  
How to verify: stress-run с enqueue  
Notes: базовый дефект “нулевой доставки” устранен, но под тяжелым профилем остаются `late/unknown ack` и `timeout failed`.

14. Overflow policy (`reject` / `drop_oldest`)  
Status: `done`  
Evidence: `internal/session/manager.go`, `internal/server/e2e_test.go`  
How to verify: `go test ./internal/server -run TestE2EQueueOverflow -v`

## D. Персистентность

15. State-file хранение и восстановление после рестарта  
Status: `done`  
Evidence: `internal/store/store.go`, `internal/server/e2e_test.go`  
How to verify: `go test ./internal/server -run TestE2ERestartRecoveryFromFileStore -v`

16. Атомарная запись state  
Status: `done`  
Evidence: `internal/store/store.go`  
How to verify: code review + store tests

17. Поведение при corrupt state-файле  
Status: `done`  
Evidence: `internal/store/store.go`  
How to verify: `go test ./internal/store -v`

## E. Observability и debug

18. `/healthz`, `/readyz`, `/metrics`  
Status: `done`  
Evidence: `internal/server/observability.go`  
How to verify: `make smoke` (при запущенном адаптере)

19. `/debug/enqueue`, `/debug/queues` при `TCPADAPTER_DEBUG=true`  
Status: `done`  
Evidence: `internal/server/observability.go`  
How to verify: curl-запросы из `README.md`

20. Метрики очередей/ACK/in-flight/IP  
Status: `done`  
Evidence: `internal/server/observability.go`  
How to verify: `curl -fsS http://127.0.0.1:18080/metrics`

## F. Нагрузка и производительность

21. Capacity по соединениям (`1000` conn)  
Status: `done`  
Evidence: `artifacts/stress_report_20260217_003425.txt`  
How to verify:
```bash
ENQUEUE_LOAD=false SIM_CLIENTS=1000 DURATION_SEC=30 make stress-run
```

22. Command-load baseline (релевантный)  
Status: `partial`  
Evidence: `artifacts/stress_report_*.txt`  
How to verify:
```bash
ENQUEUE_LOAD=true ENQUEUE_QPS=5 ENQUEUE_TTL_SECONDS=60 DURATION_SEC=60 make stress-run
```
Notes: стабильные KPI зависят от QPS/TTL профиля; нужен финальный согласованный SLA-порог.

## G. Docker и деплой

23. Контейнеризация и compose-запуск  
Status: `done`  
Evidence: `Dockerfile`, `docker-compose.yml`, `Makefile`  
How to verify:
```bash
make docker-build
make docker-up
curl -fsS http://127.0.0.1:18080/readyz
```

## H. То, что еще остается (без Kafka исключений не закрыто)

24. Реальный Kafka producer/consumer + DLQ  
Status: `todo`

25. Полное покрытие всех команд из `Протокол.csv`  
Status: `partial`

26. Мультиверсионность протокола (runtime policy)  
Status: `todo`
