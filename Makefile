GIT_VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)

.PHONY: all upstream downstream clean

all: upstream downstream

src/version/version.go:
	git fetch --tags
	@echo 'package version\n\nvar GitVersion = "$(GIT_VERSION)"' > src/version/version.go


upstream: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	go build -o ./target/upstream src/upstream/upstream.go


downstream: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	go build -o ./target/downstream src/downstream/downstream.go


clean:
	rm -f ./target/upstream ./target/downstream src/version/version.go