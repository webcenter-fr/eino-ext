## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

ENVTEST ?= $(LOCALBIN)/setup-envtest
ENVTEST_VERSION ?= v0.24.1

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.34.0

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: check-components
check-components:
	bash scripts/check_components.sh

.PHONY: ui
ui: ## Build the approval UI into server/approval/ui/dist (committed for go:embed).
	cd server/approval/ui && npm ci && npm run build

.PHONY: test
test: envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(LOCALBIN) -p path)" go test -v ./...

.PHONY: models-dev-refresh
models-dev-refresh: ## Refresh the committed models.dev catalog snapshot.
	go generate ./libs/modelsdev

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION)

.PHONY: docker-build-mcp
docker-build-mcp: ## Build the MCP server image.
	docker build -f Dockerfile.mcp -t eino-ext-mcp:dev .

.PHONY: docker-build-approval
docker-build-approval: ## Build the approval server image.
	docker build -f Dockerfile.approval -t eino-ext-approval:dev .

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart.
	helm lint helm/eino-ext

.PHONY: helm-template
helm-template: ## Render the chart and every fixture; assert the guards.
	helm template t helm/eino-ext > /dev/null
	for f in helm/eino-ext/tests/values-*.yaml; do \
		if [ "$$f" = "helm/eino-ext/tests/values-memory-multi-replica.yaml" ]; then \
			if helm template t helm/eino-ext -f "$$f" > /dev/null 2>&1; then \
				echo "GUARD BROKEN: $$f must fail"; exit 1; \
			fi; \
		else \
			helm template t helm/eino-ext -f "$$f" > /dev/null; \
		fi; \
	done

.PHONY: helm-schema
helm-schema: ## Regenerate values.schema.json.
	dagger call helm-schema export --path helm/eino-ext/values.schema.json

.PHONY: helm-docs
helm-docs: ## Regenerate the chart README.
	dagger call helm-docs export --path helm/eino-ext/README.md

.PHONY: dagger-ci
dagger-ci: ## Run the full Dagger pipeline without publishing.
	dagger call ci --dry-run --version 0.0.0-dev export --path ./ci-out

.PHONY: dagger-test
dagger-test: ## Run the Dagger test gate and export coverage.
	dagger call test export --path ./coverage.out
