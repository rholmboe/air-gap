GIT_VERSION := $(shell git describe --tags `git rev-list --tags --max-count=1`)

# Build number file
BUILD_NUMBER_FILE := BUILD_NUMBER
BUILD_NUMBER := $(shell cat $(BUILD_NUMBER_FILE))
NEXT_BUILD_NUMBER := $(shell echo $$(($(BUILD_NUMBER)+1)))

# Add build number to Go binaries
BUILD_LDFLAGS := -X 'main.BuildNumber=$(NEXT_BUILD_NUMBER)'

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
	go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/upstream src/upstream/upstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

upstream-linux-arm64: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=arm64 go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/upstream-linux-arm64 src/upstream/upstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

upstream-linux-amd64: src/version/version.go src/upstream/upstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=amd64 go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/upstream-linux-amd64 src/upstream/upstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

downstream: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/downstream src/downstream/downstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

downstream-linux-arm64: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=arm64 go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/downstream-linux-arm64 src/downstream/downstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

downstream-linux-amd64: src/version/version.go src/downstream/downstream.go
	mkdir -p ./target
	GOOS=linux GOARCH=amd64 go build -ldflags '$(BUILD_LDFLAGS)' -o ./target/downstream-linux-amd64 src/downstream/downstream.go
	@echo $(NEXT_BUILD_NUMBER) > $(BUILD_NUMBER_FILE)

build-all: upstream downstream upstream-linux-arm64 upstream-linux-amd64 downstream-linux-arm64 downstream-linux-amd64


clean:
	rm -f ./target/upstream* ./target/downstream* src/version/version.go
	cd java-streams && mvn clean