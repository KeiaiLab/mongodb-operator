# Image URL to use all building/pushing image targets
# GHCR(GitHub Container Registry)을 단일 진실 소스로 사용. Docker Hub는 더 이상 사용하지 않음.
IMG ?= ghcr.io/keiailab/mongodb-operator:latest

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen sync-crds ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

# v1.4.3 — config/crd/bases (controller-gen 출력) 와 charts/mongodb-operator/crds
# (helm chart 번들) 의 drift 방지. v1.4.0~v1.4.2 까지 chart bundle 이 stale 한
# 상태로 남아 status 신규 필드들이 K8s API server 에 의해 silently drop 됐고,
# 이로 인해 reconcileAddShards 가 empty slice index panic 으로 영구 stuck.
.PHONY: sync-crds
sync-crds: ## Sync config/crd/bases → charts/mongodb-operator/crds (release 전 의무 — drift 차단).
	@echo "=== sync CRD bundles (config/crd/bases → charts/mongodb-operator/crds) ==="
	@cp config/crd/bases/*.yaml charts/mongodb-operator/crds/
	@echo "✓ CRD bundles synced ($$(ls -1 config/crd/bases/*.yaml | wc -l | tr -d ' ') CRDs)"

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -race ./... -coverprofile cover.out

.PHONY: test-unit
test-unit: fmt vet ## Run unit tests only (no envtest required).
	go test -race ./internal/resources/... ./internal/mongodb/... -coverprofile cover-unit.out

.PHONY: test-mongo
test-mongo: ## Run real-mongo integration tests (insights round-trip). 실 mongod 필요 — 미가용 시 자동 skip. 예: docker run -d -p 27077:27017 mongo:8.0 && INSIGHTS_TEST_MONGO_HOST=localhost:27077 make test-mongo
	INSIGHTS_TEST_MONGO_HOST=$${INSIGHTS_TEST_MONGO_HOST:-localhost:27077} go test -tags integration ./internal/insights/... -run TestIntegration -v

# ──────────────────────────────────────────────────────────────────────────
# RELEASE 자동화 (RFC 0002 release.yml + helm-publish.yml 대체)
# 사용법:
#   make release VERSION=v1.3.2-beta.6
#
# 순서: gate → image build/push → git tag → tag push → GH release → helm-publish
# 모든 단계가 PASS여야 진행. 1단계라도 실패 시 즉시 중단.
# ──────────────────────────────────────────────────────────────────────────
.PHONY: release
release: ## Full release pipeline. VERSION=v1.x.y 필수 (e.g., make release VERSION=v1.3.2-beta.6).
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수 (예- make release VERSION=v1.3.2-beta.6)"; exit 1; fi
	@echo "=== Step 1/6- 로컬 게이트 (lint/test/audit/validate) ==="
	@$(MAKE) gate
	@echo ""
	@echo "=== Step 2/6- Chart.yaml + CHANGELOG version 일치 확인 ==="
	@CHART_VER=$$(grep '^version:' charts/mongodb-operator/Chart.yaml | awk '{print $$2}'); \
	TARGET_VER=$$(echo "$(VERSION)" | sed 's/^v//'); \
	if [ "$$CHART_VER" != "$$TARGET_VER" ]; then \
		echo "ERROR- Chart.yaml version=$$CHART_VER, but release VERSION=$$TARGET_VER"; \
		echo "  먼저 charts/mongodb-operator/Chart.yaml 의 version + appVersion 갱신"; \
		exit 1; \
	fi
	@echo "✓ Chart.yaml version=$$(echo $(VERSION) | sed 's/^v//')"
	@echo ""
	@echo "=== Step 3/6- Docker image build + push (linux/amd64, default builder) ==="
	docker --context=default buildx build --platform $(PLATFORMS) \
		-t ghcr.io/keiailab/mongodb-operator:$(VERSION) \
		-t ghcr.io/keiailab/mongodb-operator:$$(echo $(VERSION) | sed 's/^v//') \
		--push .
	@echo ""
	@echo "=== Step 4/6- Git tag + push ==="
	@if git tag -l $(VERSION) | grep -q .; then \
		echo "WARN- tag $(VERSION) 이미 존재 — skip"; \
	else \
		git tag -a $(VERSION) -m "$(VERSION)"; \
	fi
	git push origin $(VERSION)
	@echo ""
	@echo "=== Step 5/6- GitHub Release (prerelease if -beta/-rc) ==="
	@PREFLAG=""; \
	case "$(VERSION)" in *beta*|*rc*|*alpha*) PREFLAG="--prerelease";; esac; \
	mkdir -p /tmp/release; \
	helm package charts/mongodb-operator -d /tmp/release/; \
	if command -v git-cliff >/dev/null 2>&1; then \
		git-cliff --strip all --tag "$(VERSION)" --unreleased > "/tmp/release-notes-$(VERSION).md" 2>/dev/null && \
			NOTES_FLAG="--notes-file /tmp/release-notes-$(VERSION).md"; \
	else \
		NOTES_FLAG="--notes \"Release $(VERSION). 변경 내역은 CHANGELOG.md 참조.\""; \
	fi; \
	SBOM_ASSET=""; \
	if command -v syft >/dev/null 2>&1; then \
		echo "=== syft SBOM 생성 (T0-2 자동화) ==="; \
		syft scan ghcr.io/keiailab/mongodb-operator:$(VERSION) -o spdx-json -q > "/tmp/mongodb-operator-$(VERSION).spdx.json" 2>/dev/null && \
			SBOM_ASSET="/tmp/mongodb-operator-$(VERSION).spdx.json"; \
	fi; \
	if gh release view $(VERSION) -R keiailab/mongodb-operator >/dev/null 2>&1; then \
		echo "WARN- GH release $(VERSION) 이미 존재 — skip"; \
	else \
		eval gh release create $(VERSION) -R keiailab/mongodb-operator $$PREFLAG \
			--title "$(VERSION)" \
			$$NOTES_FLAG \
			/tmp/release/mongodb-operator-$$(echo $(VERSION) | sed 's/^v//').tgz \
			$$SBOM_ASSET; \
	fi
	@echo ""
	@echo "=== Step 6/6- Helm chart publish to gh-pages ==="
	@$(MAKE) helm-publish
	@echo ""
	@echo "✓ Release $(VERSION) 완료"
	@echo "  - Image- ghcr.io/keiailab/mongodb-operator:$(VERSION)"
	@echo "  - GH Release- https://github.com/keiailab/mongodb-operator/releases/tag/$(VERSION)"
	@echo "  - Helm Repo- helm repo update + helm search repo keiailab/mongodb-operator"
	@echo "  - ArtifactHub은 ~30분 내 인덱스 자동 갱신"

# PGP signing 옵션 — HELM_SIGN=1 시 helm package --sign 으로 .prov 파일 생성.
HELM_SIGN     ?= 0
HELM_GPG_KEY  ?= 89A409476828CB992338C378651E51AF520BCB78
HELM_KEYRING  ?= $(HOME)/.gnupg/secring.gpg

# Build platforms — AGENTS.md §2 + RFC-0002 §2: amd64-only 강제 (multi-arch 금지).
# C-P0-12 (.lefthook.yml: platforms-amd64-guard) 가 grep 으로 multi-arch 재발 차단.
PLATFORMS     ?= linux/amd64
# docker buildx --load (used by `docker-build` for local dev) can only
# import single-arch images into the docker daemon. Auto-detect host arch
# so `make docker-build` works on both amd64 and arm64 (Apple Silicon).
LOCAL_PLATFORM ?= linux/$(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/' -e 's/arm64/arm64/')

.PHONY: release-notes
release-notes: ## git-cliff 로 release notes 자동 생성 — /tmp/release-notes-VERSION.md.
	@command -v git-cliff >/dev/null 2>&1 || { echo "[error] git-cliff not installed: brew install git-cliff"; exit 1; }
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	git-cliff --strip all --tag "$(VERSION)" --unreleased > "/tmp/release-notes-$(VERSION).md"
	@echo "✓ release notes: /tmp/release-notes-$(VERSION).md"

.PHONY: bundle
bundle: ## OperatorHub.io bundle 생성 — operator-sdk + kustomize. VERSION 필수 (e.g. 1.5.0). PR-B9 / ADR-0023 / ADR-0028.
	@command -v operator-sdk >/dev/null 2>&1 || { echo "[error] operator-sdk not installed: brew install operator-sdk"; exit 1; }
	@command -v kustomize >/dev/null 2>&1 || { echo "[error] kustomize not installed"; exit 1; }
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수 (e.g. make bundle VERSION=1.5.0)"; exit 1; fi
	@echo "=== set image ghcr.io/keiailab/mongodb-operator=ghcr.io/keiailab/mongodb-operator:v$(VERSION) ==="
	# 실 manager.yaml 의 image 명 (ghcr.io/keiailab/mongodb-operator) 를 정확히 타겟해야 newTag 가 적용됨.
	# 'controller' placeholder 는 manager.yaml 에 없으므로 매칭 실패 → 이전 cycle 의 version drift 원인 (CSV containerImage 가 stale tag 유지).
	cd config/manager && kustomize edit set image ghcr.io/keiailab/mongodb-operator=ghcr.io/keiailab/mongodb-operator:v$(VERSION)
	@echo "=== bump base CSV containerImage annotation to v$(VERSION) ==="
	# ADR-0028: operator-sdk 는 input CSV 의 containerImage annotation 이 이미 있으면 deployment spec 의 image 로 덮어쓰지 않는다.
	# 이 sed 단계가 없으면 base CSV 의 stale tag 가 그대로 bundle 로 흘러간다 (결격 1 RCA).
	sed -i.bak -E 's|(containerImage: ghcr.io/keiailab/mongodb-operator:)v[0-9]+\.[0-9]+\.[0-9]+.*|\1v$(VERSION)|' config/manifests/bases/mongodb-operator.clusterserviceversion.yaml
	rm -f config/manifests/bases/mongodb-operator.clusterserviceversion.yaml.bak
	@echo "=== kustomize build config/manifests | operator-sdk generate bundle ==="
	# ADR-0028: stable + alpha 양 channel 동시 push, default = stable (외부 사용자 운영 수준).
	kustomize build config/manifests | operator-sdk generate bundle \
		--overwrite \
		--version "$(VERSION)" \
		--channels stable,alpha \
		--default-channel stable \
		--package mongodb-operator
	@echo "=== operator-sdk bundle validate (operatorhub + community suite) ==="
	operator-sdk bundle validate ./bundle --select-optional suite=operatorframework
	@echo "✓ bundle: ./bundle/ ($(VERSION), channels=stable+alpha, default=stable)"

.PHONY: bundle-build
bundle-build: bundle ## bundle image 빌드 — registry push 는 community-operators PR 시점에 별.
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	# ADR-0028: GOVERNANCE.md §2 — 멀티아키 빌드 금지. PLATFORMS=linux/amd64 강제.
	docker buildx build --platform linux/amd64 -f bundle.Dockerfile -t ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION) --load .
	@echo "✓ bundle image: ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION) (local docker daemon)"

.PHONY: bundle-push
bundle-push: ## bundle image push to ghcr.io. ADR-0028 외부 사용자 운영 수준 + community-operators PR 입력.
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	docker push ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION)
	@DIGEST=$$(docker manifest inspect ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION) | python3 -c "import sys,json; m=json.load(sys.stdin); print(m['manifests'][0]['digest']) if 'manifests' in m else print(m.get('config',{}).get('digest',''))"); \
	echo "✓ bundle pushed: ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION)@$$DIGEST"

.PHONY: catalog-build
catalog-build: ## FBC catalog image 빌드. deploy/catalog/ 의 plain YAML 을 opm serve image 로 wrap. ADR-0028 Phase D.
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	# bundle 의 라이브 digest 를 catalog.yaml 의 image 필드에 자동 주입 (mutable tag 보호).
	@BUNDLE_DIGEST=$$(docker manifest inspect ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION) 2>/dev/null | python3 -c "import sys,json; m=json.load(sys.stdin); print(m.get('config',{}).get('digest') or m['manifests'][0]['digest'])"); \
	if [ -z "$$BUNDLE_DIGEST" ]; then echo "ERROR: bundle image v$(VERSION) 의 GHCR 의 digest 가져오지 못함 — bundle-push 먼저 실행"; exit 1; fi; \
	echo "=== bundle digest: $$BUNDLE_DIGEST"; \
	sed -i.bak -E "s|image: ghcr.io/keiailab/mongodb-operator-bundle:v[0-9.]+(@sha256:[a-f0-9]+)?|image: ghcr.io/keiailab/mongodb-operator-bundle:v$(VERSION)@$$BUNDLE_DIGEST|g" deploy/catalog/catalog/mongodb-operator/catalog.yaml; \
	rm -f deploy/catalog/catalog/mongodb-operator/catalog.yaml.bak
	cd deploy/catalog && docker buildx build --platform linux/amd64 -t ghcr.io/keiailab/mongodb-operator-catalog:v$(VERSION) --load .
	@echo "✓ catalog image: ghcr.io/keiailab/mongodb-operator-catalog:v$(VERSION)"

.PHONY: catalog-push
catalog-push: ## catalog image push to ghcr.io. OLM v1 ClusterCatalog 의 image: 가 본 image pull.
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	docker push ghcr.io/keiailab/mongodb-operator-catalog:v$(VERSION)
	@echo "✓ catalog pushed: ghcr.io/keiailab/mongodb-operator-catalog:v$(VERSION)"

.PHONY: sbom
sbom: ## syft 로 SBOM (SPDX-2.3) 생성 — image 의 binary + Go modules. SLSA / EU CRA 표준.
	@command -v syft >/dev/null 2>&1 || { echo "[error] syft not installed: brew install syft"; exit 1; }
	@if [ -z "$(VERSION)" ]; then echo "ERROR: VERSION 필수"; exit 1; fi
	@echo "=== syft scan ghcr.io/keiailab/mongodb-operator:$(VERSION) ==="
	syft scan ghcr.io/keiailab/mongodb-operator:$(VERSION) -o spdx-json -q > "/tmp/mongodb-operator-$(VERSION).spdx.json"
	@SIZE=$$(wc -c < "/tmp/mongodb-operator-$(VERSION).spdx.json" | tr -d ' '); \
	echo "✓ SBOM: /tmp/mongodb-operator-$(VERSION).spdx.json ($$SIZE bytes)"

.PHONY: helm-cosign-sign
helm-cosign-sign: ## G-13 (cycle 19) — CloudPirates parity Cosign chart 서명. helm package 후 cosign sign-blob 으로 .sig 생성.
	@echo "=== helm cosign sign (G-13 CloudPirates parity) ==="
	@if ! command -v cosign >/dev/null 2>&1; then echo "ERROR: cosign not installed (brew install cosign)"; exit 1; fi
	@mkdir -p /tmp/release-cosign
	@helm package charts/mongodb-operator -d /tmp/release-cosign/
	@CHART=$$(ls /tmp/release-cosign/mongodb-operator-*.tgz | head -1); \
	  echo "INFO- signing $$CHART with cosign keyless (OIDC)"; \
	  COSIGN_EXPERIMENTAL=1 cosign sign-blob --yes "$$CHART" --output-signature "$$CHART.sig" --output-certificate "$$CHART.crt"; \
	  echo "INFO- signature: $$CHART.sig"; \
	  echo "INFO- certificate: $$CHART.crt"
	@echo "✓ Cosign signing complete — chart + .sig + .crt artifacts ready for publish"

.PHONY: helm-publish
helm-publish: ## Publish helm chart to gh-pages (RFC 0002 helm-publish.yml 대체 로컬 자동화). HELM_SIGN=1 시 PGP .prov 동반.
	@echo "=== helm package ==="
	@mkdir -p /tmp/release
	@if [ "$(HELM_SIGN)" = "1" ]; then \
		echo "INFO- chart 서명 활성 (PGP key $(HELM_GPG_KEY))"; \
		helm package --sign --key "$(HELM_GPG_KEY)" --keyring "$(HELM_KEYRING)" charts/mongodb-operator -d /tmp/release/; \
	else \
		helm package charts/mongodb-operator -d /tmp/release/; \
	fi
	@echo "=== gh-pages worktree ==="
	@mkdir -p /tmp/ghpages-publish
	@if [ ! -d /tmp/ghpages-publish/gh-pages ]; then \
		cd /tmp/ghpages-publish && git clone --branch gh-pages --single-branch git@github.com:keiailab/mongodb-operator.git gh-pages; \
	else \
		cd /tmp/ghpages-publish/gh-pages && git fetch origin gh-pages && git reset --hard origin/gh-pages; \
	fi
	@echo "=== copy chart + regen index ==="
	cp /tmp/release/mongodb-operator-*.tgz /tmp/ghpages-publish/gh-pages/
	@cp /tmp/release/mongodb-operator-*.tgz.prov /tmp/ghpages-publish/gh-pages/ 2>/dev/null || true
	@cp $(CURDIR)/charts/artifacthub-repo.yml /tmp/ghpages-publish/gh-pages/ 2>/dev/null || true
	cd /tmp/ghpages-publish/gh-pages && helm repo index . --merge index.yaml --url https://keiailab.github.io/mongodb-operator
	@echo "=== commit + push ==="
	@cd /tmp/ghpages-publish/gh-pages && git add -A && \
		(git diff --cached --quiet || git commit -m "chore(helm): publish $$(grep -E '^version' $(CURDIR)/charts/mongodb-operator/Chart.yaml | awk '{print $$2}')" ) && \
		git push origin gh-pages
	@echo "✓ Helm chart published. ArtifactHub은 ~30분 내 인덱싱."

.PHONY: setup
setup: ## RFC 0002 로컬 게이트 설치 (pre-commit + pre-push hook).
	@command -v pre-commit >/dev/null 2>&1 || { echo "pre-commit not installed: pip install pre-commit"; exit 1; }
	pre-commit install --hook-type pre-commit --hook-type pre-push
	@echo "✓ pre-commit + pre-push hooks installed"
	@echo "  L2 pre-push hooks- govulncheck / trivy fs / gitleaks / go test"

.PHONY: lint
lint: golangci-lint ## Run go vet + staticcheck + golangci-lint (RFC 0002 L3 lint 게이트, 3-repo 정합).
	go vet ./...
	@command -v $(GOBIN)/staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	$(GOBIN)/staticcheck ./internal/...
	"$(GOLANGCI_LINT)" run

.PHONY: audit
audit: ## govulncheck + trivy + gosec — RFC 0002 L3 security 게이트.
	@echo "=== govulncheck (call-graph CVE) ==="
	@command -v $(GOBIN)/govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	$(GOBIN)/govulncheck ./...
	@echo "=== trivy fs (lockfile + base CVE) ==="
	@command -v trivy >/dev/null 2>&1 || { echo "[error] trivy not installed: brew install trivy"; exit 1; }
	trivy fs --severity HIGH,CRITICAL --exit-code 1 --ignore-unfixed --skip-dirs vendor,bin,tmp .
	@echo "=== gosec (HIGH only) ==="
	@command -v $(GOBIN)/gosec >/dev/null 2>&1 || go install github.com/securego/gosec/v2/cmd/gosec@latest
	$(GOBIN)/gosec -quiet -severity high ./internal/... || true

.PHONY: kube-lint
kube-lint: ## kube-linter 로 helm chart 렌더 산출물의 K8s 리소스 보안/best-practice 점검 (구 GHA kube-linter.yml 대체).
	@command -v kube-linter >/dev/null 2>&1 || { echo "[error] kube-linter not installed: brew install kube-linter (또는 go install golang.stackrox.io/kube-linter/cmd/kube-linter@latest)"; exit 1; }
	@command -v helm >/dev/null 2>&1 || { echo "[error] helm not installed"; exit 1; }
	@echo "=== kube-linter lint helm template (default values) ==="
	helm template mongodb-operator charts/mongodb-operator --include-crds | kube-linter lint -
	@echo "OK kube-linter PASS"

.PHONY: go-licenses
go-licenses: ## Go 의존성 라이선스 검사 — forbidden/restricted 라이선스 차단 (구 GHA go-licenses.yml 대체).
	@command -v go-licenses >/dev/null 2>&1 || { echo "[error] go-licenses not installed: go install github.com/google/go-licenses@latest"; exit 1; }
	@echo "=== go-licenses check (forbidden + restricted) ==="
	go-licenses check ./... --disallowed_types=forbidden,restricted
	@echo "OK go-licenses PASS"

.PHONY: md-link-check
md-link-check: ## 마크다운 문서 깨진 링크 검사 (구 GHA markdown-link-check.yml 대체).
	@command -v markdown-link-check >/dev/null 2>&1 || { echo "[error] markdown-link-check not installed: npm install -g markdown-link-check"; exit 1; }
	@echo "=== markdown-link-check (README/CHANGELOG/docs) ==="
	@fail=0; for f in README.md CHANGELOG.md $$(find docs -name '*.md' 2>/dev/null); do \
		[ -f "$$f" ] || continue; \
		markdown-link-check -q "$$f" || fail=1; \
	done; \
	[ "$$fail" = "0" ] || { echo "ERROR 깨진 markdown link 발견"; exit 1; }
	@echo "OK markdown-link-check PASS"

.PHONY: validate
validate: manifests generate ## Validate K8s manifests + helm chart + CRD (RFC 0002 L3 validate 게이트).
	helm lint charts/mongodb-operator
	helm template gate charts/mongodb-operator >/dev/null && echo "helm template OK"
	helm lint charts/mongodb-cluster
	helm template gate-cluster charts/mongodb-cluster >/dev/null && echo "mongodb-cluster template OK"
	helm template gate-cluster charts/mongodb-cluster --set architecture=sharded >/dev/null && echo "mongodb-cluster sharded template OK"

.PHONY: gate
gate: lint test-unit audit validate ## RFC 0002 로컬 4계층 게이트 — pre-push 동등.
	@echo ""
	@echo "✓ All RFC 0002 local gates passed"

.PHONY: test-integration
test-integration: manifests generate fmt vet envtest ## Run integration tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -race ./internal/controller/... -v -coverprofile cover-integration.out

## Envtest K8s version
ENVTEST_K8S_VERSION ?= 1.31.0

##@ E2E Testing (iteration 10, Phase 1 M2)

KIND ?= kind
KIND_CLUSTER ?= mongodb-operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist.
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run e2e tests against the Kind cluster.
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v
	$(MAKE) cleanup-test-e2e

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests.
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager (host arch via $(LOCAL_PLATFORM), --load can't do manifest lists).
	# 글로벌 §2: docker buildx의 기본 빌더(default)만 사용. 커스텀 빌더 인스턴스 금지.
	docker buildx build --platform $(LOCAL_PLATFORM) --load -t ${IMG} .

.PHONY: docker-push
docker-push: ## Build and push docker image ($(PLATFORMS), default builder).
	docker buildx build --platform $(PLATFORMS) --push -t ${IMG} .

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/default | kubectl delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUBECTL ?= kubectl
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.3.0
CONTROLLER_TOOLS_VERSION ?= v0.17.0
ENVTEST_VERSION ?= release-0.19
GOLANGCI_LINT_VERSION ?= v2.8.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary (3-repo 정합 — postgres/valkey 패턴).
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# go-install-tool will 'go install' any package with custom target and target directory
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
}
endef
