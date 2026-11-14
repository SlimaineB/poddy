# Makefile for poddy: build, test, container image, and k3s Helm deployment.
#
# Requires: go, podman, helm, k3s (containerd), sudo for k3s ctr image import.
# Override: make IMAGE_TAG=my-tag deploy

GO          ?= go
PODMAN      ?= podman
HELM        ?= helm
K3S         ?= k3s
SUDO        ?= sudo -n
MKDOCS      ?= mkdocs
REGISTRY    ?= ghcr.io/slimaineb
IMAGE       ?= $(REGISTRY)/poddy
IMAGE_TAG   ?= access-$(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
PULL_POLICY ?= IfNotPresent
CHART       ?= deploy/helm/poddy
VALUES_DIR  ?= /tmp/opencode
KUBECONFIG  := /tmp/opencode/k3s.yaml

HUB_RELEASE := poddy
HUB_NS      := poddy-system
REGIONS     := poddy-eu poddy-us

TEMPLATES_CHART := deploy/helm/poddy-templates
TEMPLATE_NAMES  := base go node python

export KUBECONFIG

.PHONY: all build test vet fmt lint docs image values-hub values-eu values-us \
        deploy deploy-hub deploy-eu deploy-us deploy-regions kubeconfig status clean help \
        sync-templates check-templates

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

docs:
	$(MKDOCS) build --strict

kubeconfig: $(KUBECONFIG)

$(KUBECONFIG):
	$(SUDO) cp /etc/rancher/k3s/k3s.yaml "$@"
	$(SUDO) chown $$USER:$$USER "$@"

image: kubeconfig
	$(PODMAN) build -t $(IMAGE):$(IMAGE_TAG) .
	$(PODMAN) save -o $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar $(IMAGE):$(IMAGE_TAG)
	$(SUDO) $(K3S) ctr images import $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar

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

clean:
	rm -f $(VALUES_DIR)/poddy-$(IMAGE_TAG).tar $(VALUES_DIR)/poddy-values.yaml \
	      $(VALUES_DIR)/poddy-eu-values.yaml $(VALUES_DIR)/poddy-us-values.yaml

help:
	@echo "poddy Makefile targets:"
	@echo "  make build        - go build ./..."
	@echo "  make test         - go test ./..."
	@echo "  make vet          - go vet ./..."
	@echo "  make fmt          - gofmt -w ."
	@echo "  make docs         - mkdocs build --strict"
	@echo "  make sync-templates - copy built-in templates into poddy-templates chart"
	@echo "  make check-templates - verify built-in templates are in sync"
	@echo "  make image        - build podman image and import into k3s containerd"
	@echo "  make deploy-hub   - helm upgrade hub release (poddy-system)"
	@echo "  make deploy-eu    - helm upgrade eu region"
	@echo "  make deploy-us    - helm upgrade us region"
	@echo "  make deploy       - build image + deploy hub then regions"
	@echo "  make status       - list helm releases and poddy deployments"
	@echo ""
	@echo "Variables: IMAGE_TAG (default: access-<git>), KUBECONFIG, REGISTRY, IMAGE"
