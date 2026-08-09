#!/usr/bin/env bash
# Execute the complete local runtime-acceptance profile and write a sanitized
# capability manifest. The profile is intentionally disposable: application
# journeys use a dedicated Compose project, Kubernetes uses a dedicated kind
# cluster, and the DR drill names its own volumes and backup repository.
#
# This is a repository/runtime gate, not a production-readiness claim. The
# manifest proves that every capability reached a real running boundary on the
# reviewed checkout; cloud apply, a real vendor, protected-registry admission,
# and production owner sign-off remain separate gates.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PROJECT_NAME="${RUNTIME_ACCEPTANCE_PROJECT:-seev-runtime-acceptance}"
RUN_ID="$(date -u +%Y%m%dT%H%M%SZ)-$$"
EVIDENCE_DIR="${RUNTIME_ACCEPTANCE_DIR:-${TMPDIR:-/tmp}/seev-runtime-acceptance-${RUN_ID}}"
MANIFEST="$EVIDENCE_DIR/runtime-manifest.txt"
KIND_CLUSTER="${RUNTIME_ACCEPTANCE_CLUSTER:-seev-runtime-acceptance}"
KIND_CONFIG="${KIND_CONFIG:-$ROOT_DIR/deploy/kubernetes/kind-config-single-node.yaml}"
KEEP_K8S_CLUSTER="${RUNTIME_KEEP_K8S_CLUSTER:-0}"
# Keep the disposable dependency containers away from a developer's ordinary
# stack. The application processes already use their dedicated E2E ports; the
# dependency ports need explicit isolation because Compose publishes them.
export POSTGRES_PORT="${RUNTIME_POSTGRES_PORT:-25433}"
export REDIS_PORT="${RUNTIME_REDIS_PORT:-26380}"
export RABBITMQ_PORT="${RUNTIME_RABBITMQ_PORT:-25672}"
export RABBITMQ_MANAGEMENT_PORT="${RUNTIME_RABBITMQ_MANAGEMENT_PORT:-25673}"
export REDIS_HOST_PORT="$REDIS_PORT"

if [[ "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
	cat <<'EOF'
Usage: scripts/runtime-acceptance.sh

Runs the complete disposable local runtime-acceptance profile and writes:
  runtime-manifest.txt under RUNTIME_ACCEPTANCE_DIR (or /tmp)

The profile requires Docker, Go, kind, kubectl, Helm, jq, and the local
Kubernetes image/build prerequisites. It refuses to reuse a runtime Compose
project or kind cluster and uses alternate dependency host ports so the
ordinary developer stack may remain up. Set RUNTIME_KEEP_K8S_CLUSTER=1 to
retain the dedicated kind cluster for inspection after a successful run.
EOF
	exit 0
fi

case "$PROJECT_NAME" in
	seev-runtime-acceptance|seev-runtime-acceptance-*) ;;
	*)
		printf 'runtime-acceptance: refusing unsafe Compose project name: %s\n' "$PROJECT_NAME" >&2
		exit 2
		;;
esac

if [[ -e "$MANIFEST" ]]; then
	printf 'runtime-acceptance: evidence directory already contains %s\n' "$MANIFEST" >&2
	exit 2
fi
mkdir -p "$EVIDENCE_DIR"

export COMPOSE_PROJECT_NAME="$PROJECT_NAME"
export CLUSTER_NAME="$KIND_CLUSTER"

started_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
completed=0
k8s_cluster_reserved=0
if git diff --quiet && git diff --cached --quiet; then
	working_tree="clean"
else
	working_tree="dirty"
fi

printf 'schema=seev.runtime-acceptance.v1\n' >"$MANIFEST"
printf 'run_id=%s\n' "$RUN_ID" >>"$MANIFEST"
printf 'commit=%s\n' "$(git rev-parse HEAD)" >>"$MANIFEST"
printf 'working_tree=%s\n' "$working_tree" >>"$MANIFEST"
printf 'profile=local-disposable\n' >>"$MANIFEST"
printf 'compose_project=%s\n' "$PROJECT_NAME" >>"$MANIFEST"
printf 'kubernetes_cluster=%s\n' "$KIND_CLUSTER" >>"$MANIFEST"
printf 'started_at=%s\n' "$started_at" >>"$MANIFEST"
printf 'overall=running\n' >>"$MANIFEST"

reset_compose_stack() {
	docker compose --project-name "$PROJECT_NAME" --profile app down -v --remove-orphans >/dev/null 2>&1 || true
}

cleanup() {
	local exit_code=$?
	if [[ "$completed" -ne 1 ]]; then
		printf 'finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$MANIFEST" 2>/dev/null || true
		printf 'overall=failed\n' >>"$MANIFEST" 2>/dev/null || true
	fi
	reset_compose_stack
	if [[ "$k8s_cluster_reserved" -eq 1 && "$KEEP_K8S_CLUSTER" != "1" ]] && command -v kind >/dev/null 2>&1; then
		kind delete cluster --name "$KIND_CLUSTER" >/dev/null 2>&1 || true
	fi
	printf 'runtime-acceptance: manifest=%s\n' "$MANIFEST"
	return "$exit_code"
}
trap cleanup EXIT INT TERM

for command in docker make go curl openssl kind kubectl helm jq; do
	command -v "$command" >/dev/null 2>&1 || {
		printf 'runtime-acceptance: required command is missing: %s\n' "$command" >&2
		exit 2
	}
done
docker compose version >/dev/null 2>&1 || {
	printf 'runtime-acceptance: Docker Compose v2 is required\n' >&2
	exit 2
}

existing_project_containers="$(docker compose --project-name "$PROJECT_NAME" ps -aq 2>/dev/null || true)"
if [[ -n "$existing_project_containers" ]]; then
	printf 'runtime-acceptance: refusing to reuse existing Compose project %s; remove it explicitly first\n' "$PROJECT_NAME" >&2
	exit 1
fi

record_stage() {
	local name="$1" status="$2" command_text="$3"
	printf 'stage\t%s\t%s\t%s\n' "$name" "$status" "$command_text" >>"$MANIFEST"
}

run_stage() {
	local name="$1" description="$2" command_text="$3"
	shift 3
	printf '\n=== runtime acceptance: %s ===\n' "$description"
	printf 'runtime-acceptance: %s\n' "$command_text"
	if "$@"; then
		record_stage "$name" passed "$command_text"
		printf 'runtime-acceptance: %s passed\n' "$name"
		return 0
	fi
	record_stage "$name" failed "$command_text"
	printf 'runtime-acceptance: %s failed\n' "$name" >&2
	return 1
}

run_host_stage() {
	reset_compose_stack
	run_stage "$@"
	reset_compose_stack
}

run_dr_drills() {
	local dr_dir="$EVIDENCE_DIR/dr"
	mkdir -p "$dr_dir"
	make certs backup-secret
	docker compose --project-name "$PROJECT_NAME" build postgres

	AUTH_APP_PORT="${RUNTIME_DR_AUTH_APP_PORT:-28082}" \
	AUTH_INTERNAL_PORT="${RUNTIME_DR_AUTH_INTERNAL_PORT:-28083}" \
	DRILL_ALLOW_SHARED_DEV_STACK=1 \
	GAMEDAY_PROJECT="${PROJECT_NAME}-gameday-latest" \
	DRILL_PROJECT_NAME="${PROJECT_NAME}-drill-latest" \
	GAMEDAY_REPO_PATH="$dr_dir/latest-repo" \
	DRILL_REPORT_PATH="$dr_dir/latest.json" \
		"$ROOT_DIR/scripts/dr-drill.sh" latest

	AUTH_APP_PORT="${RUNTIME_DR_AUTH_APP_PORT:-28082}" \
	AUTH_INTERNAL_PORT="${RUNTIME_DR_AUTH_INTERNAL_PORT:-28083}" \
	DRILL_ALLOW_SHARED_DEV_STACK=1 \
	GAMEDAY_PROJECT="${PROJECT_NAME}-gameday-pitr" \
	DRILL_PROJECT_NAME="${PROJECT_NAME}-drill-pitr" \
	GAMEDAY_REPO_PATH="$dr_dir/pitr-repo" \
	DRILL_REPORT_PATH="$dr_dir/pitr.json" \
		"$ROOT_DIR/scripts/dr-drill.sh" pitr
}

prepare_k8s_cluster() {
	if kind get clusters 2>/dev/null | grep -Fxq "$KIND_CLUSTER"; then
		printf 'runtime-acceptance: refusing to reuse existing kind cluster: %s\n' "$KIND_CLUSTER" >&2
		exit 1
	fi
	k8s_cluster_reserved=1
}

# Application runtime: each journey gets fresh databases and a fresh host
# process set. The core and capability chaos runs cover the failure/recovery
# boundary for every application capability in the inventory.
run_stage repository-unit "repository unit and architecture tests" "GOCACHE=/tmp/seev-go-cache go test ./..." env GOCACHE="${GOCACHE:-/tmp/seev-go-cache}" go test ./...
run_host_stage app-business "business money lifecycle" "make business-e2e" make business-e2e
run_host_stage app-merchant "merchant tenant lifecycle" "make merchant-e2e" make merchant-e2e
run_host_stage app-capability "scheduled, fee, disbursement, dispute, reconciliation, and FX journeys" "make capability-e2e" make capability-e2e
run_host_stage app-privacy "auth privacy and retention lifecycle" "make privacy-e2e" make privacy-e2e
run_host_stage app-core-chaos "core dependency and recovery matrix" "KEEP_WORK_DIR=1 ./scripts/chaos-test.sh all" env KEEP_WORK_DIR=1 "$ROOT_DIR/scripts/chaos-test.sh" all
run_host_stage app-capability-chaos "capability fail-closed and recovery matrix" "make capability-chaos" make capability-chaos

# Container/runtime boundary and deployment contracts.
run_host_stage container "container image round-trip" "make smoke-container" env SEEV_SMOKE_ARTIFACT_DIR="$EVIDENCE_DIR/container-failure" make smoke-container
run_stage platform-integration "Terraform and Helm integration contracts" "make platform-integration-check" make platform-integration-check
run_stage platform-e2e "rendered workload and Gateway platform journey" "make platform-e2e" make platform-e2e
run_stage platform-chaos "removed workload-identity fault" "make platform-chaos" make platform-chaos
run_stage supply-chain "source and protected-release evidence verifier" "make supply-chain-e2e" make supply-chain-e2e

# Kubernetes runtime: the cluster is dedicated and deleted by the EXIT trap
# unless the operator explicitly requests retention for inspection.
prepare_k8s_cluster
export KIND_CONFIG
run_stage kubernetes-e2e "disposable kind edge and business journey" "make k8s-e2e" make k8s-e2e
run_stage kubernetes-chaos "Gateway pod replacement and recovered edge" "make k8s-chaos" make k8s-chaos

# DR owns its own Compose projects and destroys only their named volumes.
run_stage disaster-recovery "latest and PITR restore game days" "scripts/dr-drill.sh latest + pitr" run_dr_drills

# The checker deliberately owns this list too: a green command with a missing
# capability row must not be sufficient to mark the inventory accepted.
record_capability() {
	local capability="$1" evidence="$2"
	printf 'capability\t%s\taccepted\t%s\n' "$capability" "$evidence" >>"$MANIFEST"
}
record_capability "Ledger posting and idempotency" "app-business,app-merchant,app-core-chaos"
record_capability "Unified command policy boundary" "app-capability,app-capability-chaos"
record_capability "KYC/tenant execution state" "app-business,app-merchant,app-capability-chaos"
record_capability "Scheduled and recurring transactions" "app-capability,app-capability-chaos"
record_capability "Fee pricing and approvals" "app-capability,app-capability-chaos"
record_capability "Pay-in and callback reconciliation" "app-business,app-merchant,app-core-chaos"
record_capability "Payout unknown-state recovery" "app-capability,app-capability-chaos"
record_capability "Disbursement maker-checker" "app-capability,app-capability-chaos"
record_capability "Chargeback dispute lifecycle" "app-capability,app-capability-chaos"
record_capability "Reconciliation and append-only corrections" "app-capability,app-capability-chaos"
record_capability "Multi-currency and FX" "app-capability,app-capability-chaos"
record_capability "Transactional outbox" "app-business,app-merchant,app-core-chaos"
record_capability "Auth and KYC" "app-business,app-privacy,app-capability-chaos"
record_capability "Container/Kubernetes deployment" "container,kubernetes-e2e,kubernetes-chaos"
record_capability "IaC and workload identity" "platform-integration,platform-e2e,platform-chaos"
record_capability "Supply-chain controls" "supply-chain"
record_capability "DR and restore" "disaster-recovery"

printf 'finished_at=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >>"$MANIFEST"
printf 'overall=passed\n' >>"$MANIFEST"
"$ROOT_DIR/scripts/ci/check-runtime-acceptance.sh" "$MANIFEST"
completed=1
printf 'runtime-acceptance: complete local profile passed for all 17 capabilities\n'
