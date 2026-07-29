#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
test_root="$(mktemp -d /tmp/verlay-two-device.XXXXXX)"
bin_dir="$test_root/bin"
relay_pid=""
node_a_pid=""
node_b_pid=""

cleanup() {
  for pid in "$node_a_pid" "$node_b_pid" "$relay_pid"; do
    [[ -z "$pid" ]] || kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
trap cleanup EXIT

fail() {
  printf 'FAIL: %s\nArtifacts: %s\n' "$*" "$test_root" >&2
  exit 1
}

json_string() {
  local field="$1"
  sed -n 's/.*"'"$field"'":"\([^"]*\)".*/\1/p'
}

wait_for() {
  local label="$1" expected="$2" output=""
  shift 2
  for _ in $(seq 1 120); do
    output="$("$@" 2>/dev/null || true)"
    [[ "$output" == *"$expected"* ]] && return 0
    sleep 0.25
  done
  fail "$label: expected $expected, last response: $output"
}

node_a() {
  env "${node_a_env[@]}" "$bin_dir/verlay" "$@"
}

node_b() {
  env "${node_b_env[@]}" "$bin_dir/verlay" "$@"
}

start_relay() {
  "$bin_dir/croc" relay --host 127.0.0.1 --ports 9009,9010,9011,9012,9013 \
    >>"$test_root/relay.log" 2>&1 &
  relay_pid=$!
  sleep 0.4
  kill -0 "$relay_pid" 2>/dev/null || fail "local relay did not start; ports 9009-9013 may be busy"
}

stop_relay() {
  [[ -z "$relay_pid" ]] || kill "$relay_pid" 2>/dev/null || true
  [[ -z "$relay_pid" ]] || wait "$relay_pid" 2>/dev/null || true
  relay_pid=""
}

start_node_a() {
  env "${node_a_env[@]}" "$bin_dir/verlay" daemon run >>"$test_root/node-a.log" 2>&1 &
  node_a_pid=$!
  wait_for "Node A startup" '"ok":true' node_a --json status
}

start_node_b() {
  env "${node_b_env[@]}" "$bin_dir/verlay" daemon run >>"$test_root/node-b.log" 2>&1 &
  node_b_pid=$!
  wait_for "Node B startup" '"ok":true' node_b --json status
}

stop_node_a() {
  [[ -z "$node_a_pid" ]] || kill "$node_a_pid" 2>/dev/null || true
  [[ -z "$node_a_pid" ]] || wait "$node_a_pid" 2>/dev/null || true
  node_a_pid=""
}

stop_node_b() {
  [[ -z "$node_b_pid" ]] || kill "$node_b_pid" 2>/dev/null || true
  [[ -z "$node_b_pid" ]] || wait "$node_b_pid" 2>/dev/null || true
  node_b_pid=""
}

assert_same_file() {
  local source="$1" received="$2"
  [[ -f "$received" ]] || fail "received file is missing: $received"
  [[ "$(sha256sum "$source" | cut -d' ' -f1)" == "$(sha256sum "$received" | cut -d' ' -f1)" ]] ||
    fail "SHA-256 mismatch: $source != $received"
}

mkdir -p "$bin_dir"
cd "$repo_root"
go build -o "$bin_dir/verlay" ./cmd/verlay
go build -o "$bin_dir/croc" .

for node in a b; do
  mkdir -p "$test_root/$node/config/mesh" "$test_root/$node/data" \
    "$test_root/$node/run" "$test_root/$node/tmp" "$test_root/$node/inbox"
  printf '{"relayAddress":"127.0.0.1:9009","receiveDir":"%s"}\n' \
    "$test_root/$node/inbox" >"$test_root/$node/config/mesh/config.json"
  chmod 600 "$test_root/$node/config/mesh/config.json"
done

node_a_env=(
  "XDG_CONFIG_HOME=$test_root/a/config"
  "XDG_DATA_HOME=$test_root/a/data"
  "XDG_RUNTIME_DIR=$test_root/a/run"
  "TMPDIR=$test_root/a/tmp"
  "VERLAY_DEVICE_NAME=Node-A"
)
node_b_env=(
  "XDG_CONFIG_HOME=$test_root/b/config"
  "XDG_DATA_HOME=$test_root/b/data"
  "XDG_RUNTIME_DIR=$test_root/b/run"
  "TMPDIR=$test_root/b/tmp"
  "VERLAY_DEVICE_NAME=Node-B"
)

start_relay
start_node_a
start_node_b

printf '1/7 pairing survives restart\n'
pairing_json="$(node_a --json add)"
pairing_code="$(printf '%s' "$pairing_json" | json_string code)"
[[ -n "$pairing_code" ]] || fail "Node A did not return a pairing code"
stop_node_a
start_node_a
reused_code="$(node_a --json add | json_string code)"
[[ "$reused_code" == "$pairing_code" ]] || fail "active pairing code changed after restart"
node_b --json --timeout 30s add "$pairing_code" >/dev/null
wait_for "Node A pairing" '"pairedDevices":1' node_a --json status

printf '2/7 automatic file delivery and hash verification\n'
dd if=/dev/urandom of="$test_root/auto.bin" bs=1M count=8 status=none
auto_json="$(node_a --json --timeout 60s send "$test_root/auto.bin" --to Node-B)"
auto_id="$(printf '%s' "$auto_json" | json_string transferId)"
auto_received="$(node_b --json transfers "$auto_id" | json_string savedPath)"
assert_same_file "$test_root/auto.bin" "$auto_received"

printf '3/7 bidirectional delivery\n'
printf 'reply from node b\n' >"$test_root/reply.txt"
reply_json="$(node_b --json --timeout 60s send "$test_root/reply.txt" --to Node-A)"
reply_id="$(printf '%s' "$reply_json" | json_string transferId)"
reply_received="$(node_a --json transfers "$reply_id" | json_string savedPath)"
assert_same_file "$test_root/reply.txt" "$reply_received"

printf '4/7 folder tree and exclusion\n'
mkdir -p "$test_root/project/src" "$test_root/project/empty" "$test_root/project/.git"
printf 'package main\n' >"$test_root/project/src/main.go"
printf 'excluded\n' >"$test_root/project/.git/secret"
folder_json="$(node_a --json --timeout 60s send "$test_root/project" --to Node-B --exclude .git)"
folder_id="$(printf '%s' "$folder_json" | json_string transferId)"
folder_received="$(node_b --json transfers "$folder_id" | json_string savedPath)"
[[ -f "$folder_received/src/main.go" && -d "$folder_received/empty" && ! -e "$folder_received/.git" ]] ||
  fail "received folder tree or exclusion is incorrect"

printf '5/7 manual approval and rejection\n'
node_b --json receive --ask Node-A >/dev/null
printf 'approve me\n' >"$test_root/manual.txt"
manual_json="$(node_a --json --background send "$test_root/manual.txt" --to Node-B)"
manual_id="$(printf '%s' "$manual_json" | json_string transferId)"
wait_for "manual approval offer" '"status":"pending_approval"' node_b --json transfers "$manual_id"
node_b --json receive "$manual_id" >/dev/null
wait_for "manual transfer completion" '"status":"completed"' node_a --json transfers "$manual_id"
manual_received="$(node_b --json transfers "$manual_id" | json_string savedPath)"
assert_same_file "$test_root/manual.txt" "$manual_received"

printf 'reject me\n' >"$test_root/reject.txt"
reject_json="$(node_a --json --background send "$test_root/reject.txt" --to Node-B)"
reject_id="$(printf '%s' "$reject_json" | json_string transferId)"
wait_for "rejection offer" '"status":"pending_approval"' node_b --json transfers "$reject_id"
node_b --json receive "$reject_id" --reject >/dev/null
wait_for "sender rejection state" '"status":"rejected"' node_a --json transfers "$reject_id"
node_b --json receive --always Node-A >/dev/null

printf '6/7 receiver restart and durable queue retry\n'
stop_node_b
printf 'queued while offline\n' >"$test_root/offline.txt"
offline_json="$(node_a --json --background send "$test_root/offline.txt" --to Node-B)"
offline_id="$(printf '%s' "$offline_json" | json_string transferId)"
start_node_b
wait_for "offline transfer recovery" '"status":"completed"' node_a --json transfers "$offline_id"
offline_received="$(node_b --json transfers "$offline_id" | json_string savedPath)"
assert_same_file "$test_root/offline.txt" "$offline_received"

printf '7/7 relay interruption and automatic reconnect\n'
stop_relay
printf 'queued while relay is offline\n' >"$test_root/relay-retry.txt"
relay_json="$(node_a --json --background send "$test_root/relay-retry.txt" --to Node-B)"
relay_id="$(printf '%s' "$relay_json" | json_string transferId)"
sleep 0.5
start_relay
wait_for "relay reconnect transfer" '"status":"completed"' node_a --json transfers "$relay_id"
relay_received="$(node_b --json transfers "$relay_id" | json_string savedPath)"
assert_same_file "$test_root/relay-retry.txt" "$relay_received"

node_a --json doctor | grep -q '"healthy":true' || fail "Node A doctor failed"
node_b --json doctor | grep -q '"healthy":true' || fail "Node B doctor failed"
printf 'PASS: real two-node pairing, transfer, policy, restart, reconnect, and hashes\n'
printf 'Artifacts: %s\n' "$test_root"
