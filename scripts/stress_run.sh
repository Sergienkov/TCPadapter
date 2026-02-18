#!/usr/bin/env bash
set -euo pipefail

# Reproducible local stress scenario without Kafka.
# Produces:
# - artifacts/stress_metrics_<ts>.prom
# - artifacts/stress_report_<ts>.txt

export LC_ALL=C

ADAPTER_ADDR="${ADAPTER_ADDR:-:15010}"
METRICS_ADDR="${METRICS_ADDR:-:18080}"
SIM_ADDR="${SIM_ADDR:-$ADAPTER_ADDR}"
SIM_CLIENTS="${SIM_CLIENTS:-100}"
DURATION_SEC="${DURATION_SEC:-30}"
SIM_BASE_IMEI="${SIM_BASE_IMEI:-}"
SETTLE_SEC="${SETTLE_SEC:-12}"
SETTLE_MAX_SEC="${SETTLE_MAX_SEC:-60}"

ACK_DELAY="${ACK_DELAY:-250ms}"
ACK_ERROR_RATE="${ACK_ERROR_RATE:-0.10}"
ACK_DROP_RATE="${ACK_DROP_RATE:-0.05}"
STATUS_BURST_SIZE="${STATUS_BURST_SIZE:-4}"
STATUS_BURST_SPACING="${STATUS_BURST_SPACING:-20ms}"
STATUS_INTERVAL="${STATUS_INTERVAL:-2s}"
WARMUP_SEC="${WARMUP_SEC:-20}"
WARMUP_ONLINE_RATIO="${WARMUP_ONLINE_RATIO:-95}"
STATE_FILE="${STATE_FILE:-}"
ACK_TIMEOUT_QUERY="${ACK_TIMEOUT_QUERY:-8s}"
RETRY_BACKOFF_QUERY="${RETRY_BACKOFF_QUERY:-2s}"
MAX_RETRIES_QUERY="${MAX_RETRIES_QUERY:-2}"

# Optional command injection load (uses /debug/enqueue).
ENQUEUE_LOAD="${ENQUEUE_LOAD:-false}"
ENQUEUE_QPS="${ENQUEUE_QPS:-10}"
ENQUEUE_CMD_ID="${ENQUEUE_CMD_ID:-9}"
ENQUEUE_TTL_SECONDS="${ENQUEUE_TTL_SECONDS:-30}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACTS_DIR="$ROOT_DIR/artifacts"
mkdir -p "$ARTIFACTS_DIR"

TS="$(date +%Y%m%d_%H%M%S)"
RUN_START_EPOCH="$(date +%s)"
if [[ -z "$SIM_BASE_IMEI" ]]; then
  # Default to a run-specific IMEI range to reduce collisions between parallel runs.
  SIM_BASE_IMEI="$((860000000000000 + ($(date +%s) % 100000)))"
fi
METRICS_FILE="$ARTIFACTS_DIR/stress_metrics_${TS}.prom"
REPORT_FILE="$ARTIFACTS_DIR/stress_report_${TS}.txt"
ADAPTER_LOG="$ARTIFACTS_DIR/stress_adapter_${TS}.log"
SIM_LOG="$ARTIFACTS_DIR/stress_sim_${TS}.log"
if [[ -z "$STATE_FILE" ]]; then
  STATE_FILE="$ARTIFACTS_DIR/stress_state_${TS}.json"
fi
ENQUEUE_ATTEMPT_FILE="$ARTIFACTS_DIR/stress_enqueue_attempted_${TS}.tmp"
ENQUEUE_ACCEPT_FILE="$ARTIFACTS_DIR/stress_enqueue_accepted_${TS}.tmp"
: >"$ENQUEUE_ATTEMPT_FILE"
: >"$ENQUEUE_ACCEPT_FILE"

adapter_pid=""
sim_pid=""
enqueue_pid=""

normalize_http_addr() {
  local raw="$1"
  if [[ "$raw" == :* ]]; then
    echo "127.0.0.1${raw}"
    return
  fi
  echo "$raw"
}

extract_port() {
  local raw="$1"
  if [[ "$raw" == *:* ]]; then
    echo "${raw##*:}"
    return
  fi
  echo ""
}

ensure_port_free() {
  local addr="$1"
  local label="$2"
  local port
  port="$(extract_port "$addr")"
  if [[ -z "$port" ]]; then
    return
  fi
  if command -v lsof >/dev/null 2>&1; then
    if lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
      echo "[stress] ${label} port ${port} is already in use"
      echo "[stress] stop old adapter/simulator processes or choose other ports"
      exit 1
    fi
  fi
}

ratio_pct() {
  local num="$1"
  local den="$2"
  LC_ALL=C awk -v n="${num}" -v d="${den}" 'BEGIN{ if (d <= 0) { printf "0.00" } else { printf "%.2f", (100.0*n)/d } }'
}

cleanup() {
  if [[ -n "$enqueue_pid" ]] && kill -0 "$enqueue_pid" 2>/dev/null; then
    kill "$enqueue_pid" 2>/dev/null || true
    wait "$enqueue_pid" 2>/dev/null || true
  fi
  if [[ -n "$sim_pid" ]] && kill -0 "$sim_pid" 2>/dev/null; then
    kill "$sim_pid" 2>/dev/null || true
    wait "$sim_pid" 2>/dev/null || true
  fi
  if [[ -n "$adapter_pid" ]] && kill -0 "$adapter_pid" 2>/dev/null; then
    kill "$adapter_pid" 2>/dev/null || true
    wait "$adapter_pid" 2>/dev/null || true
  fi
}
trap cleanup EXIT

ensure_port_free "$ADAPTER_ADDR" "adapter"
ensure_port_free "$METRICS_ADDR" "metrics"

echo "[stress] starting adapter on $ADAPTER_ADDR (metrics $METRICS_ADDR)"
adapter_debug="false"
if [[ "$ENQUEUE_LOAD" == "true" ]]; then
  adapter_debug="true"
fi
(
  cd "$ROOT_DIR"
  TCPADAPTER_LISTEN_ADDR="$ADAPTER_ADDR" \
  TCPADAPTER_METRICS_ADDR="$METRICS_ADDR" \
  TCPADAPTER_DEBUG="$adapter_debug" \
  TCPADAPTER_STATE_FILE="$STATE_FILE" \
  TCPADAPTER_ACK_TIMEOUT_QUERY="$ACK_TIMEOUT_QUERY" \
  TCPADAPTER_RETRY_BACKOFF_QUERY="$RETRY_BACKOFF_QUERY" \
  TCPADAPTER_MAX_RETRIES_QUERY="$MAX_RETRIES_QUERY" \
  go run ./cmd/tcpadapter >"$ADAPTER_LOG" 2>&1
) &
adapter_pid="$!"

HTTP_METRICS_ADDR="$(normalize_http_addr "$METRICS_ADDR")"
METRICS_URL="http://${HTTP_METRICS_ADDR}/metrics"
READY_URL="http://${HTTP_METRICS_ADDR}/readyz"
ENQUEUE_URL="http://${HTTP_METRICS_ADDR}/debug/enqueue"

deadline=$((SECONDS + 20))
ready_wait_start=$SECONDS
until curl -fsS "$READY_URL" >/dev/null 2>&1; do
  if ! kill -0 "$adapter_pid" 2>/dev/null; then
    echo "[stress] adapter exited before ready; tail log:"
    tail -n 40 "$ADAPTER_LOG" || true
    exit 1
  fi
  if (( SECONDS >= deadline )); then
    echo "[stress] adapter did not become ready in time"
    tail -n 40 "$ADAPTER_LOG" || true
    exit 1
  fi
  sleep 0.2
done
ready_wait_sec=$((SECONDS - ready_wait_start))

if ! kill -0 "$adapter_pid" 2>/dev/null; then
  echo "[stress] adapter process is not running after ready check; tail log:"
  tail -n 40 "$ADAPTER_LOG" || true
  exit 1
fi

echo "[stress] starting simulator: clients=$SIM_CLIENTS duration=${DURATION_SEC}s"
(
  cd "$ROOT_DIR"
  go run ./cmd/controller-sim \
    --addr "$SIM_ADDR" \
    --clients "$SIM_CLIENTS" \
    --base-imei "$SIM_BASE_IMEI" \
    --status-interval "$STATUS_INTERVAL" \
    --ack-delay "$ACK_DELAY" \
    --ack-error-rate "$ACK_ERROR_RATE" \
    --ack-drop-rate "$ACK_DROP_RATE" \
    --status-burst-size "$STATUS_BURST_SIZE" \
    --status-burst-spacing "$STATUS_BURST_SPACING" >"$SIM_LOG" 2>&1
) &
sim_pid="$!"

if [[ "$WARMUP_SEC" =~ ^[0-9]+$ ]] && (( WARMUP_SEC > 0 )); then
  warmup_wait_start=$SECONDS
  target_online=$(( (SIM_CLIENTS * WARMUP_ONLINE_RATIO + 99) / 100 ))
  if (( target_online < 1 )); then
    target_online=1
  fi
  if (( target_online > SIM_CLIENTS )); then
    target_online=$SIM_CLIENTS
  fi

  echo "[stress] warmup: waiting up to ${WARMUP_SEC}s for online_sessions>=${target_online}/${SIM_CLIENTS}"
  warmup_deadline=$((SECONDS + WARMUP_SEC))
  while (( SECONDS < warmup_deadline )); do
    online_sessions="$(curl -fsS "$METRICS_URL" 2>/dev/null | awk '$1=="tcpadapter_online_sessions_total"{print $2}' | tail -n1)"
    if [[ "$online_sessions" =~ ^[0-9]+$ ]] && (( online_sessions >= target_online )); then
      echo "[stress] warmup reached: online_sessions=${online_sessions}"
      break
    fi
    sleep 1
  done
  warmup_wait_sec=$((SECONDS - warmup_wait_start))
else
  warmup_wait_sec=0
fi

load_start_sec=$SECONDS
if [[ "$ENQUEUE_LOAD" == "true" ]]; then
  echo "[stress] starting enqueue load: qps=$ENQUEUE_QPS cmd=$ENQUEUE_CMD_ID ttl=${ENQUEUE_TTL_SECONDS}s"
  (
    i=0
    sleep_s="0.10"
    if [[ "$ENQUEUE_QPS" =~ ^[0-9]+$ ]] && (( ENQUEUE_QPS > 0 )); then
      sleep_s="$(LC_ALL=C awk -v q="$ENQUEUE_QPS" 'BEGIN{ printf "%.3f", 1.0/q }')"
      sleep_s="${sleep_s/,/.}"
    fi
    while true; do
      idx=$(( i % SIM_CLIENTS ))
      controller_num=$((SIM_BASE_IMEI + idx))
      controller_id="$(printf "%015d" "$controller_num")"
      msg_id="stress-${TS}-${i}"
      trace_id="stress-trace-${TS}-${i}"
      echo 1 >>"$ENQUEUE_ATTEMPT_FILE"
      if curl -fsS --connect-timeout 0.2 --max-time 0.8 -X POST "$ENQUEUE_URL" \
        -H 'Content-Type: application/json' \
        -d "{\"controller_id\":\"${controller_id}\",\"command_id\":${ENQUEUE_CMD_ID},\"ttl_seconds\":${ENQUEUE_TTL_SECONDS},\"message_id\":\"${msg_id}\",\"trace_id\":\"${trace_id}\"}" >/dev/null 2>&1; then
        echo 1 >>"$ENQUEUE_ACCEPT_FILE"
      else
        :
      fi
      i=$((i+1))
      sleep "$sleep_s"
    done
  ) &
  enqueue_pid="$!"
fi

sleep "$DURATION_SEC"
load_run_sec=$((SECONDS - load_start_sec))

if [[ -n "$enqueue_pid" ]] && kill -0 "$enqueue_pid" 2>/dev/null; then
  kill "$enqueue_pid" 2>/dev/null || true
  wait "$enqueue_pid" 2>/dev/null || true
  enqueue_pid=""
fi

if [[ "$SETTLE_SEC" =~ ^[0-9]+$ ]] && (( SETTLE_SEC > 0 )); then
  settle_wait_start=$SECONDS
  echo "[stress] settle period: ${SETTLE_SEC}s (draining in-flight/acks)"
  sleep "$SETTLE_SEC"
  settle_wait_sec=$((SECONDS - settle_wait_start))
else
  settle_wait_sec=0
fi

if [[ "$SETTLE_MAX_SEC" =~ ^[0-9]+$ ]] && (( SETTLE_MAX_SEC > 0 )); then
  drain_wait_start=$SECONDS
  echo "[stress] drain wait: up to ${SETTLE_MAX_SEC}s for queue_depth_sum=0 and inflight_sum=0"
  drain_deadline=$((SECONDS + SETTLE_MAX_SEC))
  while (( SECONDS < drain_deadline )); do
    metrics_snapshot="$(curl -fsS "$METRICS_URL" 2>/dev/null || true)"
    q_sum="$(printf "%s\n" "$metrics_snapshot" | awk '$1=="tcpadapter_queue_depth_sum"{print $2}' | tail -n1)"
    i_sum="$(printf "%s\n" "$metrics_snapshot" | awk '$1=="tcpadapter_inflight_sum"{print $2}' | tail -n1)"
    if [[ "$q_sum" =~ ^[0-9]+$ ]] && [[ "$i_sum" =~ ^[0-9]+$ ]]; then
      if (( q_sum == 0 && i_sum == 0 )); then
        echo "[stress] drain complete: queue_depth_sum=0 inflight_sum=0"
        break
      fi
    fi
    sleep 1
  done
  drain_wait_sec=$((SECONDS - drain_wait_start))
else
  drain_wait_sec=0
fi

echo "[stress] collecting metrics"
curl -fsS "$METRICS_URL" >"$METRICS_FILE"

active_connections="$(awk '$1=="tcpadapter_active_connections"{print $2}' "$METRICS_FILE" | tail -n1)"
sessions_total="$(awk '$1=="tcpadapter_sessions_total"{print $2}' "$METRICS_FILE" | tail -n1)"
ack_accepted="$(awk '$1 ~ /^tcpadapter_ack_total\{status="accepted"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_delivered="$(awk '$1 ~ /^tcpadapter_ack_total\{status="delivered"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_failed="$(awk '$1 ~ /^tcpadapter_ack_total\{status="failed"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_expired="$(awk '$1 ~ /^tcpadapter_ack_total\{status="expired"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_retrying="$(awk '$1 ~ /^tcpadapter_ack_total\{status="retrying"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_unsupported="$(awk '$1 ~ /^tcpadapter_ack_total\{status="unsupported"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ack_duplicate="$(awk '$1 ~ /^tcpadapter_ack_total\{status="duplicate"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ip_rejected_blocked="$(awk '$1 ~ /^tcpadapter_ip_rejected_total\{reason="blocked"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
ip_rejected_per_ip="$(awk '$1 ~ /^tcpadapter_ip_rejected_total\{reason="per_ip_limit"\}/{print $2}' "$METRICS_FILE" | tail -n1)"
queue_depth_sum="$(awk '$1=="tcpadapter_queue_depth_sum"{print $2}' "$METRICS_FILE" | tail -n1)"
queue_depth_max="$(awk '$1=="tcpadapter_queue_depth_max"{print $2}' "$METRICS_FILE" | tail -n1)"
inflight_sum="$(awk '$1=="tcpadapter_inflight_sum"{print $2}' "$METRICS_FILE" | tail -n1)"

n_accepted="${ack_accepted:-0}"
n_delivered="${ack_delivered:-0}"
n_failed="${ack_failed:-0}"
n_expired="${ack_expired:-0}"
n_retrying="${ack_retrying:-0}"
n_unsupported="${ack_unsupported:-0}"
n_duplicate="${ack_duplicate:-0}"
n_terminal=$((n_delivered + n_failed + n_expired + n_unsupported + n_duplicate))
enqueue_attempted_total="$(wc -l <"$ENQUEUE_ATTEMPT_FILE" | tr -d ' ')"
enqueue_accepted_total="$(wc -l <"$ENQUEUE_ACCEPT_FILE" | tr -d ' ')"
run_total_sec=$(( $(date +%s) - RUN_START_EPOCH ))

delivery_rate_pct="$(ratio_pct "$n_delivered" "$n_terminal")"
fail_rate_pct="$(ratio_pct "$n_failed" "$n_terminal")"
expired_rate_pct="$(ratio_pct "$n_expired" "$n_terminal")"
retry_ratio_pct="$(ratio_pct "$n_retrying" "$n_accepted")"
accepted_to_delivered_pct="$(ratio_pct "$n_delivered" "$n_accepted")"

{
  echo "stress_ts=$TS"
  echo "duration_sec=$DURATION_SEC"
  echo "settle_sec=$SETTLE_SEC"
  echo "settle_max_sec=$SETTLE_MAX_SEC"
  echo "sim_clients=$SIM_CLIENTS"
  echo "warmup_sec=$WARMUP_SEC"
  echo "warmup_online_ratio=$WARMUP_ONLINE_RATIO"
  echo "sim_base_imei=$SIM_BASE_IMEI"
  echo "enqueue_load=$ENQUEUE_LOAD"
  echo "enqueue_qps=$ENQUEUE_QPS"
  echo "enqueue_cmd_id=$ENQUEUE_CMD_ID"
  echo "enqueue_attempted_total=$enqueue_attempted_total"
  echo "enqueue_accepted_total=$enqueue_accepted_total"
  echo "state_file=$STATE_FILE"
  echo "ack_timeout_query=$ACK_TIMEOUT_QUERY"
  echo "retry_backoff_query=$RETRY_BACKOFF_QUERY"
  echo "max_retries_query=$MAX_RETRIES_QUERY"
  echo "adapter_addr=$ADAPTER_ADDR"
  echo "metrics_addr=$METRICS_ADDR"
  echo "active_connections=${active_connections:-0}"
  echo "sessions_total=${sessions_total:-0}"
  echo "ack_accepted=${n_accepted}"
  echo "ack_delivered=${n_delivered}"
  echo "ack_failed=${n_failed}"
  echo "ack_expired=${n_expired}"
  echo "ack_retrying=${n_retrying}"
  echo "ack_unsupported=${n_unsupported}"
  echo "ack_duplicate=${n_duplicate}"
  echo "ack_terminal_total=${n_terminal}"
  echo "kpi_delivery_rate_pct=${delivery_rate_pct}"
  echo "kpi_fail_rate_pct=${fail_rate_pct}"
  echo "kpi_expired_rate_pct=${expired_rate_pct}"
  echo "kpi_retry_ratio_pct=${retry_ratio_pct}"
  echo "kpi_accepted_to_delivered_pct=${accepted_to_delivered_pct}"
  echo "ip_rejected_blocked=${ip_rejected_blocked:-0}"
  echo "ip_rejected_per_ip=${ip_rejected_per_ip:-0}"
  echo "queue_depth_sum=${queue_depth_sum:-0}"
  echo "queue_depth_max=${queue_depth_max:-0}"
  echo "inflight_sum=${inflight_sum:-0}"
  echo "ready_wait_sec=$ready_wait_sec"
  echo "warmup_wait_sec=$warmup_wait_sec"
  echo "load_run_sec=$load_run_sec"
  echo "settle_wait_sec=$settle_wait_sec"
  echo "drain_wait_sec=$drain_wait_sec"
  echo "run_total_sec=$run_total_sec"
  echo "metrics_file=$METRICS_FILE"
  echo "adapter_log=$ADAPTER_LOG"
  echo "sim_log=$SIM_LOG"
} >"$REPORT_FILE"

echo "[stress] report: $REPORT_FILE"
cat "$REPORT_FILE"
