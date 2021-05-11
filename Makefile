CONTAINER_COMMAND = $(shell if [ -x "$(shell which docker)" ];then echo "docker" ; else echo "podman";fi)
INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/ocpmetal/assisted-installer-controller:stable)
ROOT_DIR = $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
REPORTS = $(ROOT_DIR)/reports
TEST_PUBLISH_FLAGS = --junitfile-testsuite-name=relative --junitfile-testcase-classname=relative --junitfile $(REPORTS)/unittest.xml
NAMESPACE := $(or ${NAMESPACE},assisted-installer)
GIT_REVISION := $(shell git rev-parse HEAD)
PUBLISH_TAG := $(or ${GIT_REVISION})

CONTAINER_BUILD_PARAMS = --network=host --label git_revision=${GIT_REVISION}

all: lint format-check build-images unit-test

lint:
	golangci-lint run -v

format:
	@goimports -w -l src/ || /bin/true

format-check:
	@test -z $(shell $(MAKE) format)

generate:
	go generate $(shell go list ./...)
	$(MAKE) format

unit-test: $(REPORTS)
	gotestsum --format=pkgname $(TEST_PUBLISH_FLAGS) -- -cover -coverprofile=$(REPORTS)/coverage.out $(or ${TEST},$(shell go list ./...)) -ginkgo.focus=${FOCUS} -ginkgo.v
	gocov convert $(REPORTS)/coverage.out | gocov-xml > $(REPORTS)/coverage.xml

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
