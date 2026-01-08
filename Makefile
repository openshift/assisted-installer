CONTAINER_COMMAND = $(shell if [ -x "$(shell which docker)" ];then echo "docker" ; else echo "podman";fi)
INSTALLER := $(or ${INSTALLER},quay.io/edge-infrastructure/assisted-installer:latest)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/edge-infrastructure/assisted-installer-controller:latest)
ROOT_DIR = $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
NAMESPACE := $(or ${NAMESPACE},assisted-installer)
GIT_REVISION := $(shell git rev-parse HEAD)

CONTAINER_BUILD_PARAMS = --network=host --label git_revision=${GIT_REVISION}

REPORTS ?= $(ROOT_DIR)/reports
CI ?= false
TEST_FORMAT ?= standard-verbose
GOTEST_FLAGS = --format=$(TEST_FORMAT) -- -count=1 -cover -coverprofile=$(REPORTS)/$(TEST_SCENARIO)_coverage.out
GINKGO_FLAGS = -ginkgo.focus="$(FOCUS)" -ginkgo.v -ginkgo.skip="$(SKIP)" -ginkgo.reportFile=./junit_$(TEST_SCENARIO)_test.xml

# Multiarch support.  Transform argument passed from docker buidx tool to go build arguments to support cross compiling
GO_BUILD_ARCHITECTURE_VARS := $(if ${TARGETPLATFORM},$(shell echo ${TARGETPLATFORM} | awk -F / '{printf("GOOS=%s GOARCH=%s", $$1, $$2)}'),)
GO_BUILD_VARS = 
ifeq ($(TARGETPLATFORM),linux/amd64)
	GO_BUILD_VARS = CGO_ENABLED=1 $(GO_BUILD_ARCHITECTURE_VARS)
else
	GO_BUILD_VARS = CGO_ENABLED=0 $(GO_BUILD_ARCHITECTURE_VARS)
endif


all: lint format-check build-images unit-test

lint: vendor-diff
	golangci-lint run -v

vendor-diff:
	go mod vendor && git diff --exit-code vendor

format:
	@goimports -w -l src/ || /bin/true

format-check:
	@test -z $(shell $(MAKE) format)

generate:
	go generate $(shell go list ./...)
	$(MAKE) format

unit-test:
	$(MAKE) _test TEST_SCENARIO=unit TIMEOUT=30m TEST="$(or $(TEST),$(shell go list ./...))" || ($(CONTAINER_COMMAND) kill postgres && /bin/false)

_test: $(REPORTS)
	gotestsum $(GOTEST_FLAGS) $(TEST) $(GINKGO_FLAGS) -timeout $(TIMEOUT) || ($(MAKE) _post_test && /bin/false)
	$(MAKE) _post_test

_post_test: $(REPORTS)
	@for name in `find '$(ROOT_DIR)' -name 'junit*.xml' -type f -not -path '$(REPORTS)/*'`; do \
		mv -f $$name $(REPORTS)/junit_$(TEST_SCENARIO)_$$(basename $$(dirname $$name)).xml; \
	done
	$(MAKE) _coverage

_coverage: $(REPORTS)
ifeq ($(CI), true)
	gocov convert $(REPORTS)/$(TEST_SCENARIO)_coverage.out | gocov-xml > $(REPORTS)/$(TEST_SCENARIO)_coverage.xml
ifeq ($(TEST_SCENARIO), unit)
	COVER_PROFILE=$(REPORTS)/$(TEST_SCENARIO)_coverage.out ./hack/publish-codecov.sh
endif
endif

build: installer controller

installer:
	$(GO_BUILD_VARS) go build -o build/installer src/main/main.go

controller:
	$(GO_BUILD_VARS) go build -o build/assisted-installer-controller src/main/assisted-installer-controller/assisted_installer_main.go

build-images: installer-image controller-image

installer-image:
	$(CONTAINER_COMMAND) build $(CONTAINER_BUILD_PARAMS) -f Dockerfile.assisted-installer . -t $(INSTALLER)

controller-image:
	$(CONTAINER_COMMAND) build $(CONTAINER_BUILD_PARAMS) -f Dockerfile.assisted-installer-controller . -t $(CONTROLLER)

push-installer: installer-image
	$(CONTAINER_COMMAND) push $(INSTALLER)

push-controller: controller-image
	$(CONTAINER_COMMAND) push $(CONTROLLER)

$(REPORTS):
	-mkdir -p $(REPORTS)

clean:
	-rm -rf build $(REPORTS)
