BINARY_NAME=logaround
GOBIN?=$(shell go env GOBIN)
ifeq ($(GOBIN),)
	GOBIN=$(HOME)/go/bin
endif

.PHONY: all build clean install

build:
	go build -o $(BINARY_NAME) ./cmd

install: build
	mkdir -p $(GOBIN)
	cp $(BINARY_NAME) $(GOBIN)/$(BINARY_NAME)
	@echo "Installed $(BINARY_NAME) to $(GOBIN)"

clean:
	go clean
	rm -f $(BINARY_NAME)

all: build 