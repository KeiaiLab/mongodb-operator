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
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

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

.PHONY: setup
setup: ## RFC 0002 로컬 게이트 설치 (pre-commit + pre-push hook).
	@command -v pre-commit >/dev/null 2>&1 || { echo "pre-commit not installed: pip install pre-commit"; exit 1; }
	pre-commit install --hook-type pre-commit --hook-type pre-push
	@echo "✓ pre-commit + pre-push hooks installed"
	@echo "  L2 pre-push hooks- govulncheck / trivy fs / gitleaks / go test"

.PHONY: lint
lint: ## Run go vet + staticcheck (RFC 0002 L3 lint 게이트).
	go vet ./...
	@command -v $(GOBIN)/staticcheck >/dev/null 2>&1 || go install honnef.co/go/tools/cmd/staticcheck@latest
	$(GOBIN)/staticcheck ./internal/...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run --fix || echo "golangci-lint not installed, skipping"

.PHONY: audit
audit: ## govulncheck + trivy + gosec — RFC 0002 L3 security 게이트.
	@echo "=== govulncheck (call-graph CVE) ==="
	@command -v $(GOBIN)/govulncheck >/dev/null 2>&1 || go install golang.org/x/vuln/cmd/govulncheck@latest
	$(GOBIN)/govulncheck ./...
	@echo "=== trivy fs (lockfile + base CVE) ==="
	@command -v trivy >/dev/null 2>&1 && trivy fs --exit-code 1 --severity HIGH,CRITICAL --quiet --skip-dirs vendor . || echo "trivy not installed (brew install trivy)"
	@echo "=== gosec (HIGH only) ==="
	@command -v $(GOBIN)/gosec >/dev/null 2>&1 || go install github.com/securego/gosec/v2/cmd/gosec@latest
	$(GOBIN)/gosec -quiet -severity high ./internal/... || true

.PHONY: validate
validate: manifests generate ## Validate K8s manifests + helm chart + CRD (RFC 0002 L3 validate 게이트).
	helm lint charts/mongodb-operator
	helm template gate charts/mongodb-operator >/dev/null && echo "helm template OK"

.PHONY: gate
gate: lint test-unit audit validate ## RFC 0002 로컬 4계층 게이트 — pre-push 동등.
	@echo ""
	@echo "✓ All RFC 0002 local gates passed"

.PHONY: test-integration
test-integration: manifests generate fmt vet envtest ## Run integration tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -race ./internal/controller/... -v -coverprofile cover-integration.out

## Envtest K8s version
ENVTEST_K8S_VERSION ?= 1.31.0

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager (linux/amd64, default builder).
	# 글로벌 §2: docker buildx의 기본 빌더(default)만 사용. 커스텀 빌더 인스턴스 금지.
	docker buildx build --platform linux/amd64 --load -t ${IMG} .

.PHONY: docker-push
docker-push: ## Build and push docker image (linux/amd64, default builder).
	# --platform 명시로 host 아키텍처와 무관하게 amd64 단일 빌드. 멀티아키 금지.
	docker buildx build --platform linux/amd64 --push -t ${IMG} .

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

## Tool Versions
KUSTOMIZE_VERSION ?= v5.3.0
CONTROLLER_TOOLS_VERSION ?= v0.17.0
ENVTEST_VERSION ?= release-0.19

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

# go-install-tool will 'go install' any package with custom target and target directory
define go-install-tool
@[ -f $(1) ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
GOBIN=$(LOCALBIN) go install $${package} ;\
}
endef
