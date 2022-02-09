CONTAINER_COMMAND = $(shell if [ -x "$(shell which docker)" ];then echo "docker" ; else echo "podman";fi)
INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/ocpmetal/assisted-installer-controller:stable)
ROOT_DIR = $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
NAMESPACE := $(or ${NAMESPACE},assisted-installer)
GIT_REVISION := $(shell git rev-parse HEAD)
PUBLISH_TAG := $(or ${GIT_REVISION})

CONTAINER_BUILD_PARAMS = --network=host --label git_revision=${GIT_REVISION}

REPORTS ?= $(ROOT_DIR)/reports
CI ?= false
TEST_FORMAT ?= standard-verbose
GOTEST_FLAGS = --format=$(TEST_FORMAT) -- -count=1 -cover -coverprofile=$(REPORTS)/$(TEST_SCENARIO)_coverage.out
GINKGO_FLAGS = -ginkgo.focus="$(FOCUS)" -ginkgo.v -ginkgo.skip="$(SKIP)" -ginkgo.reportFile=./junit_$(TEST_SCENARIO)_test.xml

all: lint format-check build-images unit-test

ci-lint:
	${ROOT_DIR}/hack/check-commits.sh

lint: ci-lint
	golangci-lint run -v

format:
	@goimports -w -l src/ || /bin/true

format-check:
	@test -z $(shell $(MAKE) format)

generate:
	go generate $(shell go list ./...)
	$(MAKE) format

unit-test:
	$(MAKE) _test TEST_SCENARIO=unit TIMEOUT=30m TEST="$(or $(TEST),$(shell go list ./...))" || (docker kill postgres && /bin/false)

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
	CGO_ENABLED=0 go build -o build/installer src/main/main.go

controller:
	CGO_ENABLED=0 go build -o build/assisted-installer-controller src/main/assisted-installer-controller/assisted_installer_main.go

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

define publish_image
        docker tag ${1} ${2}
        docker push ${2}
endef # publish_image

publish:
	$(call publish_image,${INSTALLER},quay.io/ocpmetal/assisted-installer:${PUBLISH_TAG})
	$(call publish_image,${CONTROLLER},quay.io/ocpmetal/assisted-installer-controller:${PUBLISH_TAG})

clean:
	-rm -rf build $(REPORTS)
