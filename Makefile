INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)

all: image unit-test

lint:
	golangci-lint run -v

format:
	goimports -w -l src/

generate:
	go generate $(shell go list ./...)

unit-test: generate
	go test -v $(shell go list ./...) -cover

ut: generate
	go test -v -coverprofile=coverage.out ./... && go tool cover -html=coverage.out && rm coverage.out

build/installer: lint format
	mkdir -p build
	CGO_ENABLED=0 go build -o build/installer src/main/main.go

image: build/installer
	docker build -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: image
	docker push $(INSTALLER)

clean:
	rm -rf build

