GIT_VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)

.PHONY: all upstream downstream clean

all: upstream downstream

src/version/version.go:
	git fetch --tags
	@echo 'package version\n\nvar GitVersion = "$(GIT_VERSION)"' > src/version/version.go

upstream: src/version/version.go src/upstream/upstream.go
	cd src/upstream && go build -o upstream upstream.go

downstream: src/version/version.go src/downstream/downstream.go
	cd src/downstream && go build -o downstream downstream.go

clean:
	rm -f src/upstream/upstream src/downstream/downstream src/version/version.go