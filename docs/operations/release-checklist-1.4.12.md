# Release Checklist — v1.4.12

[production-grade-sprint.md Phase D](production-grade-sprint.md#phase-d--1412-release-pipeline-t27) 의
실행 체크리스트. 사용자 명시 승인 시 *순서대로 실행*.

## Pre-flight 검증 (외부 effect 0)

> 본 단계는 *언제든 실행 가능* — release 직전 final 점검.

```bash
# 1. 코드 영역 검증
make lint                                   # golangci-lint 0 issues
make test                                   # 22 unit + 9 ginkgo PASS
KUBEBUILDER_ASSETS=$(pwd)/bin/k8s/$(ls bin/k8s | head -1) \
    go test ./internal/webhook/v1alpha1/ -count=1   # envtest PASS

# 2. Chart 영역
helm lint charts/mongodb-operator --set webhook.enabled=true   # PASS
helm template test charts/mongodb-operator --set webhook.enabled=true >/tmp/manifest.yaml
helm install test charts/mongodb-operator --set webhook.enabled=true --dry-run >/dev/null   # NOTES 렌더링

# 3. Chart.yaml + CHANGELOG 정합
grep "version: 1.4.12" charts/mongodb-operator/Chart.yaml
grep "^## \[1.4.12\]" CHANGELOG.md
grep -E "appVersion: \"1.4.12\"" charts/mongodb-operator/Chart.yaml

# 4. git working tree clean
git status --short                          # empty
git log -1 --oneline                        # 최신 commit 확인

# 5. 운영 cluster 안정 (release 후 ArgoCD sync 진행 경로 검증)
./scripts/audit-cluster-state.sh            # All checks PASS
```

## Release pipeline (외부 effect 4건)

> 사용자 명시 승인 후 *atomic 단위* 진행. Step D2 의 `make release` 가 4
> 외부 effect 동시 진행.

```bash
# Step D1: pre-flight (위 5건 모두 PASS 확인)
# (위 명령 모두 통과 후 D2 진입)

# Step D2: release pipeline (atomic — 부분 실패 시 manual cleanup 필요)
make release VERSION=v1.4.12

# 내부 실행 순서 (Makefile :75 release target):
#   1) Chart.yaml version match check
#   2) docker buildx build --platform linux/amd64 --push
#      → ghcr.io/keiailab/mongodb-operator:v1.4.12 + :1.4.12
#   3) git tag v1.4.12 + git push origin v1.4.12
#   4) gh release create v1.4.12 + helm package upload
#   5) make helm-publish (gh-pages branch chart .tgz publish)

# Step D3: keiailab-platform-data umbrella bump (별 PR)
cd ../keiailab-platform-data
sed -i '' 's/version: "1.4.11"/version: "1.4.12"/' mongodb/Chart.yaml
sed -i '' 's/appVersion: "1.4.11"/appVersion: "1.4.12"/' mongodb/Chart.yaml
sed -i '' 's/^version: 0.1.12/version: 0.1.13/' mongodb/Chart.yaml
helm dep update mongodb/                    # Chart.lock 재생성
git add mongodb/Chart.yaml mongodb/Chart.lock
git commit -m "chore(mongodb): bump operator 1.4.11 → 1.4.12"
git push origin stable

# Step D4: ArgoCD auto-sync 확인 (~3 분)
kubectl get application -n argocd platform-data-mongodb -w
# Synced+Healthy + revision 갱신 확인.

# Step D5: rollout 검증
kubectl get deploy -n data -l app.kubernetes.io/name=mongodb-operator \
    -o jsonpath='{.items[0].spec.template.spec.containers[0].image}'
# ghcr.io/keiailab/mongodb-operator:1.4.12 확인.

# Step D6: 운영 영향 0 확인
./scripts/audit-cluster-state.sh            # All checks PASS
kubectl get mongodbsharded -n data keiailab-mongo -o jsonpath='{.status.phase}'
# Running.

# Step D7: smoke test
./scripts/release-smoke-test.sh v1.4.12
# 12 PASS / 0 FAIL.
```

## Rollback 절차 (사고 시)

D2 의 `make release` 가 *비가역* 이지만 *deployment rollback* 은 가능:

```bash
# keiailab-platform-data revert
cd ../keiailab-platform-data
git revert <bump-commit>
git push origin stable
# ArgoCD auto-sync → 1.4.11 image 회복.

# operator pod 즉시 회복 확인
kubectl rollout status -n data deploy/platform-data-mongodb-mongodb-operator --timeout=120s
```

GH Release / git tag / ghcr image 자체는 *유지* (재사용 위해). chart
gh-pages 의 `1.4.12.tgz` 제거 시 `gh release delete-asset` + gh-pages 의
`index.yaml` 수동 갱신 필요 (cleanup overhead).

## 후속 작업 (release 성공 후)

- [ ] TASKS.md T27 100% (commit hash 기록).
- [ ] HANDOFF.md release readiness 표 ⏳ → ✅.
- [ ] cluster-audit.md KPI 표 갱신 (release lag 0).
- [ ] CHANGELOG.md `[Unreleased]` 비움.
- [ ] 다음 release planning (1.4.13 또는 1.5.0) 시작.

## Risk register

| Risk | Mitigation |
|---|---|
| ghcr push 실패 (network / quota) | 재시도. quota 초과 시 사용자 승인 후 cleanup. |
| git tag 충돌 (이미 v1.4.12 존재) | `git tag -d v1.4.12` 후 재시도 (release pipeline 의 tag check 우회). |
| GH Release 본문 형식 깨짐 | 수동 편집 (`gh release edit v1.4.12`). |
| ArgoCD sync 시 webhook 등록 실패 | webhook.enabled=false default 라 영향 0. 활성화 시 cert-manager 의존성 확인. |
| smoke test 1 FAIL (SBOM 등) | 사전이슈 확인 (1.4.11 동일 케이스 — T22 SBOM 영역). 별 cycle 처리. |
