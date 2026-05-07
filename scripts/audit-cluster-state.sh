#!/usr/bin/env bash
# audit-cluster-state.sh — argos data plane cluster ops audit 자동 측정.
#
# docs/operations/cluster-audit.md 의 *KPI baseline* 측정 + Live verification
# 갱신 후보 출력. 운영자 manual 갱신 baseline (자동 commit 안 함).
#
# 사용법:
#   ./scripts/audit-cluster-state.sh
#   ./scripts/audit-cluster-state.sh --check    # KPI 충족 여부 exit code
#
# 의존성: kubectl (current-context=argos).

set -euo pipefail

DATE=$(date -u +%Y-%m-%d)
EXPECTED_CONTEXT="argos"
TARGET_NS="data"

pass() { printf "  ✅ %s\n" "$1"; }
fail() { printf "  ❌ %s\n" "$1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
warn() { printf "  ⚠️  %s\n" "$1"; }
section() { printf "\n=== %s ===\n" "$1"; }

FAIL_COUNT=0

section "Live verification (CLAUDE.md §7 게이트)"
ctx=$(kubectl config current-context 2>/dev/null || echo "")
if [[ "$ctx" == "$EXPECTED_CONTEXT" ]]; then
    pass "kube-context = $ctx"
else
    fail "kube-context = '$ctx' (expected '$EXPECTED_CONTEXT')"
fi

ns_status=$(kubectl get ns "$TARGET_NS" -o jsonpath='{.status.phase}' 2>/dev/null || echo "NotFound")
if [[ "$ns_status" == "Active" ]]; then
    pass "ns/$TARGET_NS = Active"
else
    fail "ns/$TARGET_NS = $ns_status"
fi

section "ArgoCD GitOps coverage (KPI)"
# argos-platform-data umbrella + platform-data-* sub apps 모두 카운트.
total_apps=$(kubectl get application -n argocd 2>/dev/null | grep -cE "^(platform-data-|argos-platform-data)" || true)
synced_apps=$(kubectl get application -n argocd 2>/dev/null | awk '/^(platform-data-|argos-platform-data)/ && $2=="Synced" && $3=="Healthy"' | wc -l | tr -d ' ')
echo "  data plane apps (umbrella + sub): $synced_apps/$total_apps Synced+Healthy"
if [[ "$synced_apps" -eq "$total_apps" ]] && [[ "$total_apps" -gt 0 ]]; then
    pass "ArgoCD coverage 100%"
else
    fail "ArgoCD coverage $synced_apps/$total_apps"
fi

section "3 Operator log errors (5min, KPI)"
for op in mongodb-operator valkey-operator postgres-operator; do
    errs=$(kubectl logs -n "$TARGET_NS" -l "app.kubernetes.io/name=$op" --since=5m --tail=500 2>/dev/null | grep -ciE "error|fail|panic" || true)
    if [[ "$errs" -eq 0 ]]; then
        pass "$op errors=0 (5min)"
    else
        fail "$op errors=$errs (5min)"
    fi
done

section "data ns events (1h)"
event_count=$(kubectl get events -n "$TARGET_NS" --sort-by='.lastTimestamp' 2>/dev/null | grep -c "^[a-z]" || true)
if [[ "$event_count" -le 1 ]]; then
    pass "events = $event_count (1h, ≤1 OK)"
else
    warn "events = $event_count (1h, audit 필요)"
fi

section "Operator chart version (release lag)"
mongodb_ver=$(kubectl get deploy -n "$TARGET_NS" -l "app.kubernetes.io/name=mongodb-operator" -o jsonpath='{.items[0].metadata.labels.app\.kubernetes\.io/version}' 2>/dev/null || echo "?")
valkey_ver=$(kubectl get deploy -n "$TARGET_NS" valkey-operator-prod -o jsonpath='{.metadata.labels.app\.kubernetes\.io/version}' 2>/dev/null || echo "?")
postgres_ver=$(kubectl get deploy -n "$TARGET_NS" -l "app.kubernetes.io/name=postgres-operator" -o jsonpath='{.items[0].metadata.labels.app\.kubernetes\.io/version}' 2>/dev/null || echo "?")
echo "  mongodb-operator: $mongodb_ver"
echo "  valkey-operator-prod: $valkey_ver"
echo "  postgres-operator: $postgres_ver"

section "stale ratio (cluster-audit.md live-verified 마커)"
last_verified=$(grep -oE 'live-verified: [0-9]{4}-[0-9]{2}-[0-9]{2}' docs/operations/cluster-audit.md | head -1 | awk '{print $2}' || echo "")
if [[ -n "$last_verified" ]]; then
    today_epoch=$(date -u +%s)
    last_epoch=$(date -j -f "%Y-%m-%d" "$last_verified" "+%s" 2>/dev/null || echo "$today_epoch")
    days_old=$(( (today_epoch - last_epoch) / 86400 ))
    echo "  last-verified: $last_verified (today $DATE, $days_old days)"
    if [[ "$days_old" -gt 7 ]]; then
        warn "stale: cluster-audit.md live-verified 갱신 권장 (>7일)"
    else
        pass "신선 ($days_old days, ≤7 OK)"
    fi
else
    warn "live-verified 마커 부재"
fi

section "결과"
if [[ "$FAIL_COUNT" -eq 0 ]]; then
    echo "✅ All checks PASS."
    exit 0
else
    echo "❌ $FAIL_COUNT check(s) FAILED."
    exit 1
fi
