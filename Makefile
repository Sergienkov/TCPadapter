.PHONY: test build fmt run-adapter run-sim stress stress-run smoke clean docker-build docker-up docker-up-with-sim docker-down docker-logs docker-ps

ADAPTER_ADDR ?= :15010
METRICS_ADDR ?= :18080
SIM_ADDR ?= 127.0.0.1:15010
SIM_CLIENTS ?= 10

test:
	go test ./...

build:
	go build ./cmd/tcpadapter
	go build ./cmd/controller-sim

fmt:
	gofmt -w $$(find cmd internal -name '*.go')

run-adapter:
	TCPADAPTER_DEBUG=true \
	TCPADAPTER_LISTEN_ADDR=$(ADAPTER_ADDR) \
	TCPADAPTER_METRICS_ADDR=$(METRICS_ADDR) \
	go run ./cmd/tcpadapter

run-sim:
	go run ./cmd/controller-sim \
		--addr $(SIM_ADDR) \
		--clients $(SIM_CLIENTS) \
		--status-interval 3s

stress:
	go run ./cmd/controller-sim \
		--addr $(SIM_ADDR) \
		--clients 50 \
		--status-interval 2s \
		--ack-delay 300ms \
		--ack-error-rate 0.15 \
		--ack-drop-rate 0.1 \
		--status-burst-size 4 \
		--status-burst-spacing 25ms

stress-run:
	chmod +x ./scripts/stress_run.sh
	./scripts/stress_run.sh

smoke:
	curl -fsS http://127.0.0.1$(METRICS_ADDR)/healthz
	curl -fsS http://127.0.0.1$(METRICS_ADDR)/readyz
	curl -fsS http://127.0.0.1$(METRICS_ADDR)/metrics >/dev/null
	curl -fsS -X POST http://127.0.0.1$(METRICS_ADDR)/debug/enqueue \
		-H 'Content-Type: application/json' \
		-d '{"controller_id":"860000000000000","command_id":9,"ttl_seconds":10}'
	curl -fsS http://127.0.0.1$(METRICS_ADDR)/debug/queues?limit=5 >/dev/null

clean:
	rm -f tcpadapter controller-sim

docker-build:
	docker compose build

docker-up:
	docker compose up -d adapter

docker-up-with-sim:
	docker compose --profile sim up -d

docker-down:
	docker compose down

docker-logs:
	docker compose logs -f --tail=200 adapter

docker-ps:
	docker compose ps
