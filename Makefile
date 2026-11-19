# Makefile for poddy: build, test, container image, and Helm deployments.
#
# Requires: go, podman, helm, k3s (containerd), sudo for k3s ctr image import.
# Local clusters: docker, k3d, kind, kubectl, jq, openssh.
# Override: make IMAGE_TAG=my-tag deploy

GO          ?= go
PODMAN      ?= podman
DOCKER      ?= docker
K3D         ?= k3d
KIND        ?= kind
HELM        ?= helm
K3S         ?= k3s
SUDO        ?= sudo -n
REGISTRY    ?= ghcr.io/slimaineb
IMAGE       ?= $(REGISTRY)/poddy
IMAGE_TAG   ?= access-$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PULL_POLICY ?= IfNotPresent
CHART       ?= deploy/helm/poddy
VALUES_DIR  ?= /tmp/opencode
KUBECONFIG  := /tmp/opencode/k3s.yaml

K3D_CLUSTER       ?= poddy-k3d-test
KIND_CLUSTER      ?= poddy-kind-test
KIND_NODE_IMAGE   ?= docker.io/kindest/node:v1.35.0
K3D_KUBECONFIG    ?= /tmp/poddy-k3d.kubeconfig
KIND_KUBECONFIG   ?= /tmp/poddy-kind.kubeconfig
K3D_PORTAL_PORT   ?= 18080
K3D_SSH_PORT      ?= 12222
KIND_PORTAL_PORT  ?= 18081
KIND_SSH_PORT     ?= 12223
LOCAL_IMAGE_REPO  ?= poddy
LOCAL_IMAGE_TAG   ?= local
LOCAL_IMAGE       := $(LOCAL_IMAGE_REPO):$(LOCAL_IMAGE_TAG)
WORKSPACE_IMAGE   ?= $(REGISTRY)/poddy-workspace-base:latest
POSTGRES_IMAGE    ?= docker.io/library/postgres:16-alpine

HUB_RELEASE := poddy
HUB_NS      := poddy-system
REGIONS     := poddy-eu poddy-us

TEMPLATES_CHART := deploy/helm/poddy-templates
TEMPLATE_NAMES  := base go node python

export KUBECONFIG

.PHONY: all build test vet fmt lint security docs image values-hub values-eu values-us \
        deploy deploy-hub deploy-eu deploy-us deploy-regions kubeconfig status clean help \
        sync-templates check-templates check-helm-render \
        local-image local-images k3d-up k3d-load k3d-deploy k3d-down \
        kind-up kind-load kind-deploy kind-down local-up local-deploy local-down \
        ui-test ui-test-install

all: build test vet

build:
	$(GO) build ./...

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

lint: fmt vet

security:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
	$(GO) run github.com/securego/gosec/v2/cmd/gosec@v2.30.0 -no-fail ./...
	cd website && npm audit --omit=dev --audit-level=high
	cd ui-tests && npm audit --audit-level=high
	cd vscode-extension && npm audit --omit=dev --audit-level=high

docs:
	cd website && npm ci
	cd website && npm run build

ui-test-install:
	cd ui-tests && npm ci
	cd ui-tests && npx playwright install chromium

ui-test:
	cd ui-tests && npm test

kubeconfig: $(KUBECONFIG)

$(KUBECONFIG):
	$(SUDO) cp /etc/rancher/k3s/k3s.yaml "$@"
	$(SUDO) chown $$USER:$$USER "$@"

image: kubeconfig
	$(PODMAN) build -t $(IMAGE):$(IMAGE_TAG) .
	$(PODMAN) save -o $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar $(IMAGE):$(IMAGE_TAG)
	$(SUDO) $(K3S) ctr images import $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar

local-image:
	$(DOCKER) build -t $(LOCAL_IMAGE) .

local-images: local-image
	$(DOCKER) pull $(WORKSPACE_IMAGE)
	$(DOCKER) pull $(POSTGRES_IMAGE)

k3d-up:
	@if ! $(K3D) cluster list 2>/dev/null | awk 'NR > 1 {print $$1}' | grep -qx '$(K3D_CLUSTER)'; then \
		$(K3D) cluster create $(K3D_CLUSTER) \
			--wait \
			--kubeconfig-update-default=false \
			--kubeconfig-switch-context=false; \
	fi
	@$(K3D) kubeconfig get $(K3D_CLUSTER) > $(K3D_KUBECONFIG)
	@chmod 600 $(K3D_KUBECONFIG)

k3d-load: k3d-up local-images
	$(K3D) image import -c $(K3D_CLUSTER) $(LOCAL_IMAGE) $(WORKSPACE_IMAGE) $(POSTGRES_IMAGE)

k3d-deploy: k3d-load
	PORTAL_IMAGE_REPO=$(LOCAL_IMAGE_REPO) PORTAL_IMAGE_TAG=$(LOCAL_IMAGE_TAG) \
		scripts/validate-k3d-kind.sh k3d $(K3D_KUBECONFIG) $(K3D_PORTAL_PORT) $(K3D_SSH_PORT)

k3d-down:
	@$(K3D) cluster delete $(K3D_CLUSTER) >/dev/null 2>&1 || true
	@rm -f $(K3D_KUBECONFIG)

kind-up:
	@if ! $(KIND) get clusters 2>/dev/null | grep -qx '$(KIND_CLUSTER)'; then \
		$(KIND) create cluster \
			--name $(KIND_CLUSTER) \
			--image $(KIND_NODE_IMAGE) \
			--kubeconfig $(KIND_KUBECONFIG) \
			--wait 300s; \
	fi
	@if [ ! -f $(KIND_KUBECONFIG) ]; then \
		$(KIND) export kubeconfig --name $(KIND_CLUSTER) --kubeconfig $(KIND_KUBECONFIG); \
	fi
	@chmod 600 $(KIND_KUBECONFIG)

kind-load: kind-up local-images
	$(KIND) load docker-image --name $(KIND_CLUSTER) $(LOCAL_IMAGE) $(WORKSPACE_IMAGE) $(POSTGRES_IMAGE)

kind-deploy: kind-load
	PORTAL_IMAGE_REPO=$(LOCAL_IMAGE_REPO) PORTAL_IMAGE_TAG=$(LOCAL_IMAGE_TAG) \
		scripts/validate-k3d-kind.sh kind $(KIND_KUBECONFIG) $(KIND_PORTAL_PORT) $(KIND_SSH_PORT)

kind-down:
	@$(KIND) delete cluster --name $(KIND_CLUSTER) >/dev/null 2>&1 || true
	@rm -f $(KIND_KUBECONFIG)

local-up: k3d-up kind-up

local-deploy: k3d-deploy kind-deploy

local-down: k3d-down kind-down

values-hub: kubeconfig
	$(HELM) --kubeconfig $(KUBECONFIG) -n $(HUB_NS) get values $(HUB_RELEASE) -o yaml > $(VALUES_DIR)/$(HUB_RELEASE)-values.yaml

values-eu: kubeconfig
	$(HELM) --kubeconfig $(KUBECONFIG) -n poddy-eu get values poddy-eu -o yaml > $(VALUES_DIR)/poddy-eu-values.yaml

values-us: kubeconfig
	$(HELM) --kubeconfig $(KUBECONFIG) -n poddy-us get values poddy-us -o yaml > $(VALUES_DIR)/poddy-us-values.yaml

deploy-hub: image values-hub
	$(HELM) --kubeconfig $(KUBECONFIG) upgrade --install $(HUB_RELEASE) $(CHART) \
		-n $(HUB_NS) -f $(VALUES_DIR)/$(HUB_RELEASE)-values.yaml \
		--set image.tag=$(IMAGE_TAG) --set image.pullPolicy=$(PULL_POLICY) \
		--wait --timeout 10m

# Regions must not reach PostgreSQL: they read access and quota state from the
# hub control plane. Clear any database settings inherited from older releases.
deploy-eu: image values-eu
	$(HELM) --kubeconfig $(KUBECONFIG) upgrade --install poddy-eu $(CHART) \
		-n poddy-eu -f $(VALUES_DIR)/poddy-eu-values.yaml \
		--set image.tag=$(IMAGE_TAG) --set image.pullPolicy=$(PULL_POLICY) \
		--set database.url= --set database.existingSecret= \
		--wait --timeout 10m

deploy-us: image values-us
	$(HELM) --kubeconfig $(KUBECONFIG) upgrade --install poddy-us $(CHART) \
		-n poddy-us -f $(VALUES_DIR)/poddy-us-values.yaml \
		--set image.tag=$(IMAGE_TAG) --set image.pullPolicy=$(PULL_POLICY) \
		--set database.url= --set database.existingSecret= \
		--wait --timeout 10m

deploy-regions: deploy-hub deploy-eu deploy-us

deploy: deploy-regions

status: kubeconfig
	$(HELM) --kubeconfig $(KUBECONFIG) list -A
	kubectl --kubeconfig $(KUBECONFIG) get deploy -A -l app.kubernetes.io/name=poddy

# Copy the canonical built-in templates into the poddy-templates chart so the
# chart can deploy them declaratively. Run this after editing internal/templates.
sync-templates:
	rm -rf $(TEMPLATES_CHART)/files
	mkdir -p $(TEMPLATES_CHART)/files
	for t in $(TEMPLATE_NAMES); do cp -R internal/templates/files/$$t $(TEMPLATES_CHART)/files/$$t; done

check-templates:
	@for t in $(TEMPLATE_NAMES); do \
		diff -ru internal/templates/files/$$t $(TEMPLATES_CHART)/files/$$t || { \
			echo "built-in templates out of sync; run 'make sync-templates'"; \
			exit 1; \
		}; \
	done

check-helm-render:
	python3 scripts/check-helm-render.py

clean:
	rm -f $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar $(VALUES_DIR)/poddy-values.yaml \
	      $(VALUES_DIR)/poddy-eu-values.yaml $(VALUES_DIR)/poddy-us-values.yaml

help:
	@echo "poddy Makefile targets:"
	@echo "  make build        - go build ./..."
	@echo "  make test         - go test ./..."
	@echo "  make vet          - go vet ./..."
	@echo "  make fmt          - gofmt -w ."
	@echo "  make security     - run Go and npm security scans locally"
	@echo "  make docs         - build the Astro documentation site"
	@echo "  make ui-test-install - install Playwright and Chromium"
	@echo "  make ui-test      - run Playwright UI tests"
	@echo "  make sync-templates - copy built-in templates into poddy-templates chart"
	@echo "  make check-templates - verify built-in templates are in sync"
	@echo "  make check-helm-render - verify charts render one ConfigMap per document"
	@echo "  make image        - build podman image and import into k3s containerd"
	@echo "  make local-image  - build local portal image for k3d/kind"
	@echo "  make k3d-deploy   - create k3d, load images, deploy and validate"
	@echo "  make kind-deploy  - create kind, load images, deploy and validate"
	@echo "  make local-deploy - run k3d-deploy then kind-deploy"
	@echo "  make k3d-down     - delete the k3d test cluster"
	@echo "  make kind-down    - delete the kind test cluster"
	@echo "  make local-down   - delete both local test clusters"
	@echo "  make deploy-hub   - helm upgrade hub release (poddy-system)"
	@echo "  make deploy-eu    - helm upgrade eu region"
	@echo "  make deploy-us    - helm upgrade us region"
	@echo "  make deploy       - build image + deploy hub then regions"
	@echo "  make status       - list helm releases and poddy deployments"
	@echo ""
	@echo "Variables: IMAGE_TAG, KUBECONFIG, REGISTRY, IMAGE, LOCAL_IMAGE_TAG,"
	@echo "           K3D_CLUSTER, KIND_CLUSTER, KIND_NODE_IMAGE"
