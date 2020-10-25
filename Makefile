CONTAINER_COMMAND = $(shell if [ -x "$(shell which docker)" ];then echo "docker" ; else echo "podman";fi)
INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)
GIT_REVISION := $(shell git rev-parse HEAD)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/ocpmetal/assisted-installer-controller:stable)
ROOT_DIR = $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
REPORTS = $(ROOT_DIR)/reports
TEST_PUBLISH_FLAGS = --junitfile-testsuite-name=relative --junitfile-testcase-classname=relative --junitfile $(REPORTS)/unittest.xml

all: image image_controller unit-test

lint:
	golangci-lint run -v

format:
	goimports -w -l src/ || /bin/true

generate:
	go generate $(shell go list ./...)
	$(MAKE) format

unit-test: $(REPORTS)
	gotestsum --format=pkgname $(TEST_PUBLISH_FLAGS) -- -cover -coverprofile=$(REPORTS)/coverage.out $(or ${TEST},${TEST},$(shell go list ./...)) -ginkgo.focus=${FOCUS} -ginkgo.v
	gocov convert $(REPORTS)/coverage.out | gocov-xml > $(REPORTS)/coverage.xml

build/installer: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/installer src/main/main.go

build/controller: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/assisted-installer-controller src/main/assisted-installer-controller/assisted_installer_main.go

image: build/installer
	GIT_REVISION=${GIT_REVISION} $(CONTAINER_COMMAND) build --build-arg GIT_REVISION -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: image
	$(CONTAINER_COMMAND) push $(INSTALLER)

image_controller: build/controller
	GIT_REVISION=${GIT_REVISION} $(CONTAINER_COMMAND) build --build-arg GIT_REVISION -f Dockerfile.assisted-installer-controller . -t $(CONTROLLER)

push_controller: image_controller
	$(CONTAINER_COMMAND) push $(CONTROLLER)

$(REPORTS):
	-mkdir -p $(REPORTS)

clean:
	-rm -rf build $(REPORTS)
