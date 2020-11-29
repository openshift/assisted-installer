CONTAINER_COMMAND = $(shell if [ -x "$(shell which docker)" ];then echo "docker" ; else echo "podman";fi)
INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)
GIT_REVISION := $(shell git rev-parse HEAD)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/ocpmetal/assisted-installer-controller:stable)
CONTROLLER_OCP :=  $(or ${CONTROLLER_OCP}, quay.io/ocpmetal/assisted-installer-controller-ocp:latest)
ROOT_DIR = $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
REPORTS = $(ROOT_DIR)/reports
TEST_PUBLISH_FLAGS = --junitfile-testsuite-name=relative --junitfile-testcase-classname=relative --junitfile $(REPORTS)/unittest.xml
NAMESPACE := $(or ${NAMESPACE},assisted-installer)
GIT_REVISION := $(shell git rev-parse HEAD)
PUBLISH_TAG := $(or ${GIT_REVISION})

all: image image_controller image_controller_ocp unit-test

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

build/controller_ocp: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/assisted-installer-controller-ocp src/main/assisted-installer-controller-ocp/main.go

image: build/installer
	GIT_REVISION=${GIT_REVISION} $(CONTAINER_COMMAND) build --network=host --build-arg GIT_REVISION -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: image
	$(CONTAINER_COMMAND) push $(INSTALLER)

image_controller: build/controller
	GIT_REVISION=${GIT_REVISION} $(CONTAINER_COMMAND) build --network=host --build-arg GIT_REVISION -f Dockerfile.assisted-installer-controller . -t $(CONTROLLER)

push_controller: image_controller
	$(CONTAINER_COMMAND) push $(CONTROLLER)

image_controller_ocp: build/controller_ocp
	GIT_REVISION=${GIT_REVISION} $(CONTAINER_COMMAND) build --build-arg GIT_REVISION -f Dockerfile.assisted-installer-controller-ocp . -t $(CONTROLLER_OCP)

push_controller_ocp: image_controller_ocp
	$(CONTAINER_COMMAND) push $(CONTROLLER_OCP)

deploy_controller_on_ocp_cluster:
	python3 ./deploy/assisted-installer-controller-ocp/deploy_assisted_controller.py --target "ocp" --profile "minikube" \
	    --namespace $(NAMESPACE) --inventory-url ${SERVICE_BASE_URL} --cluster-id ${CLUSTER_ID} --controller-image ${CONTROLLER_OCP}

$(REPORTS):
	-mkdir -p $(REPORTS)

define publish_image
        docker tag ${1} ${2}
        docker push ${2}
endef # publish_image

publish:
	$(call publish_image,${INSTALLER},quay.io/ocpmetal/assisted-installer:${PUBLISH_TAG})
	$(call publish_image,${CONTROLLER},quay.io/ocpmetal/assisted-installer-controller:${PUBLISH_TAG})
	$(call publish_image,${CONTROLLER_OCP},quay.io/ocpmetal/assisted-installer-controller-ocp:${PUBLISH_TAG})

clean:
	-rm -rf build $(REPORTS)
