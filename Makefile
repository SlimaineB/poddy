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

export KUBECONFIG

.PHONY: all build test vet fmt lint docs image values-hub values-eu values-us \
        deploy deploy-hub deploy-eu deploy-us deploy-regions region-db-secret kubeconfig status clean help

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

deploy-eu: image values-eu
	$(HELM) --kubeconfig $(KUBECONFIG) upgrade --install poddy-eu $(CHART) \
		-n poddy-eu -f $(VALUES_DIR)/poddy-eu-values.yaml \
		--set image.tag=$(IMAGE_TAG) --set image.pullPolicy=$(PULL_POLICY) \
		--set database.existingSecret=poddy-database \
		--wait --timeout 10m

deploy-us: image values-us
	$(HELM) --kubeconfig $(KUBECONFIG) upgrade --install poddy-us $(CHART) \
		-n poddy-us -f $(VALUES_DIR)/poddy-us-values.yaml \
		--set image.tag=$(IMAGE_TAG) --set image.pullPolicy=$(PULL_POLICY) \
		--set database.existingSecret=poddy-database \
		--wait --timeout 10m

# Copy the hub database Secret into each region so portal and bastion share the
# same access state. The URL uses the cluster FQDN and is reachable cross-namespace.
region-db-secret: kubeconfig
	@for ns in poddy-eu poddy-us; do \
		echo "syncing poddy-database into $$ns"; \
		url=$$(kubectl --kubeconfig $(KUBECONFIG) -n $(HUB_NS) get secret poddy-database -o jsonpath='{.data.database-url}' | base64 -d); \
		kubectl --kubeconfig $(KUBECONFIG) -n $$ns create secret generic poddy-database \
			--from-literal=database-url="$$url" --dry-run=client -o yaml \
		| kubectl --kubeconfig $(KUBECONFIG) apply -f -; \
	done

deploy-regions: region-db-secret deploy-hub deploy-eu deploy-us

deploy: deploy-regions

status: kubeconfig
	$(HELM) --kubeconfig $(KUBECONFIG) list -A
	kubectl --kubeconfig $(KUBECONFIG) get deploy -A -l app.kubernetes.io/name=poddy

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
	@echo "  make image        - build podman image and import into k3s containerd"
	@echo "  make deploy-hub   - helm upgrade hub release (poddy-system)"
	@echo "  make deploy-eu    - helm upgrade eu region"
	@echo "  make deploy-us    - helm upgrade us region"
	@echo "  make deploy       - build image + deploy hub then regions"
	@echo "  make region-db-secret - copy poddy-database Secret into regions"
	@echo "  make status       - list helm releases and poddy deployments"
	@echo ""
	@echo "Variables: IMAGE_TAG (default: access-<git>), KUBECONFIG, REGISTRY, IMAGE"
