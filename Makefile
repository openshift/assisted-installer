INSTALLER := $(or ${INSTALLER},quay.io/eranco74/assisted-installer:latest)

all: push

build:
	docker build -f Dockerfile.assisted-installer . -t $(INSTALLER)

push: build
	docker push $(INSTALLER)
