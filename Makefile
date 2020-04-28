INSTALLER := $(or ${INSTALLER},quay.io/ocpmetal/assisted-installer:stable)

all: image

lint:
	golangci-lint run -v

format:
	goimports -w -l cmd/ internal/

build/installer: lint
	mkdir -p build
	CGO_ENABLED=0 go build -o build/installer src/main/main.go

image: build/installer
	docker build -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: image
	docker push $(INSTALLER)

clean:
	rm -rf build

