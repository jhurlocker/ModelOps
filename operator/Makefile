IMG ?= quay.io/jhurlocker/modelops-operator:latest
VERSION ?= $(shell git describe --tags --dirty --always 2>/dev/null || echo "dev")

# Tool versions. Pinned to match sigs.k8s.io/controller-runtime v0.19.0 in
# go.mod. Invoked via `go run <module>@<version>` so they are never added
# to go.mod/go.sum as real dependencies of this module.
CONTROLLER_TOOLS_VERSION ?= v0.16.5
ENVTEST_VERSION ?= release-0.19
ENVTEST_K8S_VERSION ?= 1.31.0

LOCALBIN ?= $(shell pwd)/bin
ENVTEST_BIN_DIR ?= $(LOCALBIN)/k8s

.PHONY: all build test run docker-build docker-push manifests generate envtest-path

all: generate manifests build

build:
	CGO_ENABLED=0 go build -o bin/manager main.go

run:
	go run main.go

# Prints (and ensures downloaded) the path to the envtest KUBEBUILDER_ASSETS
# directory (etcd, kube-apiserver) for the pinned Kubernetes version.
envtest-path:
	@mkdir -p $(ENVTEST_BIN_DIR)
	@go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(ENVTEST_BIN_DIR)

test:
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@$(ENVTEST_VERSION) use $(ENVTEST_K8S_VERSION) -p path --bin-dir $(ENVTEST_BIN_DIR))" go test ./... -count=1

docker-build:
	docker build -t ${IMG} .

docker-push:
	docker push ${IMG}

# Regenerates CRD manifests (config/crd/bases) and the ClusterRole
# (config/rbac/role.yaml) from the +kubebuilder:* markers in api/v1alpha1
# and internal/controller. NOTE: gitops/components/operator/crd-*.yaml is
# a separate, manually-synced copy that ArgoCD actually deploys to the
# sandbox cluster -- after running this, diff and copy over deliberately,
# don't assume it happens automatically.
manifests:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) \
		crd:generateEmbeddedObjectMeta=false,allowDangerousTypes=true \
		rbac:roleName=modelops-operator-controller \
		paths="./..." \
		output:crd:artifacts:config=config/crd/bases \
		output:rbac:artifacts:config=config/rbac

# Regenerates zz_generated.deepcopy.go from the +kubebuilder:object:root
# markers in api/v1alpha1.
generate:
	go run sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION) \
		object:headerFile="hack/boilerplate.go.txt" \
		paths="./api/..."
