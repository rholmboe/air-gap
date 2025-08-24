GIT_VERSION := $(shell git describe --tags --always --dirty)

.PHONY: all upstream downstream clean

all: upstream downstream

src/upstream/version.go:
	@echo 'package main

var GitVersion = "$(GIT_VERSION)"' > src/upstream/version.go

upstream: src/upstream/version.go src/upstream/upstream.go
	cd src/upstream && go build -o upstream upstream.go

downstream: src/upstream/version.go src/downstream/downstream.go
	cd src/downstream && go build -o downstream downstream.go

clean:
	rm -f src/upstream/upstream src/downstream/downstream src/upstream/version.go
