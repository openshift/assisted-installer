INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)
GIT_REVISION := $(shell git rev-parse HEAD)
CONTROLLER :=  $(or ${CONTROLLER}, quay.io/ocpmetal/assisted-installer-controller:stable)
all: image image_controller unit-test

lint:
	golangci-lint run -v

format:
	goimports -w -l src/

generate:
	go generate $(shell go list ./...)
	make format

unit-test:
	go test -v $(shell go list ./...) -cover

ut:
	go test -v -coverprofile=coverage.out ./... && go tool cover -html=coverage.out && rm coverage.out

build/installer: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/installer src/main/main.go

build/controller: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/assisted-installer-controller src/main/assisted-installer-controller/assisted_installer_main.go

image: build/installer
	GIT_REVISION=${GIT_REVISION} docker build --build-arg GIT_REVISION -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: image
	docker push $(INSTALLER)

image_controller: build/controller
	GIT_REVISION=${GIT_REVISION} docker build --build-arg GIT_REVISION -f Dockerfile.assisted-installer-controller . -t $(CONTROLLER)

push_controller: image_controller
	docker push $(CONTROLLER)

clean:
	rm -rf build
