SHELL := /bin/bash
.SHELLFLAGS := -euo pipefail -c
BIN := $(CURDIR)/bin
CONTROLLER_GEN := $(BIN)/controller-gen
ENVTEST := $(BIN)/setup-envtest
KUSTOMIZE := $(BIN)/kustomize
KIND := $(BIN)/kind
KUBECTL := $(BIN)/kubectl
GOVULNCHECK := $(BIN)/govulncheck
OPA := $(BIN)/opa
IMG ?= guarded-batch-operator:dev
ENVTEST_K8S_VERSION ?= 1.37.x

.PHONY: kustomize build generate manifests fmt vet test test-integration test-e2e package-release test-release verify security policy-test image install deploy
build:
	mkdir -p $(BIN)
	CGO_ENABLED=0 go build -trimpath -o $(BIN)/manager ./cmd

generate: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt,year=2026 paths='./...'

manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths='./...' output:crd:artifacts:config=config/crd/bases

fmt:
	test -z "$$(gofmt -l api cmd internal)"

vet:
	go vet ./...

test:
	go test -race -count=1 -coverprofile=coverage.out ./...

policy-test: $(OPA)
	$(OPA) test policies -v

test-integration: manifests generate $(ENVTEST)
	KUBEBUILDER_ASSETS="$$($(ENVTEST) use '$(ENVTEST_K8S_VERSION)' --bin-dir $(BIN) -p path)" go test -race -count=1 -tags=integration ./internal/controller -v

test-e2e: build manifests $(KIND) $(KUSTOMIZE) $(KUBECTL)
	PATH="$(BIN):$$PATH" scripts/e2e.sh

package-release: build manifests $(KUSTOMIZE)
	python3 scripts/package-release.py

test-release: package-release $(KIND) $(KUBECTL)
	cd dist/release && sha256sum --check SHA256SUMS
	docker load --input dist/release/guarded-batch-operator_$$(cat VERSION)_image_linux_amd64.tar.gz
	E2E_IMAGE="ghcr.io/madhifallah/guarded-batch-operator:$$(cat VERSION)" E2E_MANIFEST="$(CURDIR)/dist/release/install.yaml" PATH="$(BIN):$$PATH" scripts/e2e.sh

verify: fmt vet test policy-test manifests generate
	git diff --exit-code -- api config/crd config/rbac/role.yaml

security: $(GOVULNCHECK)
	$(GOVULNCHECK) ./...

image:
	docker build -t $(IMG) .

install: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

deploy: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/default | kubectl apply -f -

$(BIN):
	mkdir -p $@

$(CONTROLLER_GEN): | $(BIN)
	GOBIN=$(BIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0

$(ENVTEST): | $(BIN)
	GOBIN=$(BIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.3

$(KUSTOMIZE): | $(BIN)
	GOBIN=$(BIN) go install sigs.k8s.io/kustomize/kustomize/v5@v5.8.0

$(KIND): | $(BIN)
	GOBIN=$(BIN) go install sigs.k8s.io/kind@v0.33.0

$(KUBECTL): scripts/install-kubectl.py | $(BIN)
	python3 scripts/install-kubectl.py $@

$(GOVULNCHECK): | $(BIN)
	GOBIN=$(BIN) go install golang.org/x/vuln/cmd/govulncheck@v1.9.0

$(OPA): | $(BIN)
	GOBIN=$(BIN) go install github.com/open-policy-agent/opa@v1.22.4

kustomize: $(KUSTOMIZE)
