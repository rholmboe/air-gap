GIT_VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)

.PHONY: all upstream downstream clean


.PHONY: java

all: upstream downstream upstream-linux-arm64 upstream-linux-amd64 downstream-linux-arm64 downstream-linux-amd64 java
java:
	cd java-streams && mvn clean package

src/version/version.go:
	git fetch --tags
	@echo 'package version\n\nvar GitVersion = "$(GIT_VERSION)"' > src/version/version.go


upstream: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	go build -o ./target/upstream src/upstream/upstream.go

upstream-linux-arm64: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=arm64 go build -o ./target/upstream-linux-arm64 src/upstream/upstream.go

upstream-linux-amd64: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=amd64 go build -o ./target/upstream-linux-amd64 src/upstream/upstream.go

downstream: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	go build -o ./target/downstream src/downstream/downstream.go

downstream-linux-arm64: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=arm64 go build -o ./target/downstream-linux-arm64 src/downstream/downstream.go

downstream-linux-amd64: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=amd64 go build -o ./target/downstream-linux-amd64 src/downstream/downstream.go

build-all: upstream downstream upstream-linux-arm64 upstream-linux-amd64 downstream-linux-arm64 downstream-linux-amd64


clean:
	rm -f ./target/upstream* ./target/downstream* src/version/version.go
	cd java-streams && mvn clean