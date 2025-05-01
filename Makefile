# Define the output directory and binary name
BINARY_NAME=mysql-cdc-to-http
DOCKER_TAG=mul14/$(BINARY_NAME)
OUTPUT_DIR=bin
GO_CMD=go
GO_VERSION=$(shell $(GO_CMD) version)

# Define the current platform (default to current OS and architecture)
CURRENT_GOOS=$(shell go env GOOS)
CURRENT_GOARCH=$(shell go env GOARCH)

# Define supported platforms
PLATFORMS=linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

# Default target is to build for the current OS and architecture
all: build

# Build for current platform (OS and architecture) by default
build:
	@echo "Building for current platform: $(CURRENT_GOOS)/$(CURRENT_GOARCH)"
	$(GO_CMD) build -o $(OUTPUT_DIR)/$(BINARY_NAME)-$(CURRENT_GOOS)-$(CURRENT_GOARCH) .

# Build for each platform (cross-compiling)
cross-build: $(PLATFORMS)
	@echo "Build complete for all platforms."

$(PLATFORMS):
	@echo "Building for platform: $@"
	GOOS=$(word 1, $(subst /, ,$@)) GOARCH=$(word 2, $(subst /, ,$@)) \
		$(GO_CMD) build -o $(OUTPUT_DIR)/$(BINARY_NAME)-$(word 1, $(subst /, ,$@))-$(word 2, $(subst /, ,$@)) .
	@echo "Built binary for: $@"

clean:
	@echo "Cleaning binaries..."
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-linux-amd64
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-linux-arm64
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-darwin-amd64
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-darwin-arm64
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-windows-arm64
	@rm -f $(OUTPUT_DIR)/$(BINARY_NAME)-windows-amd64
	@echo "Binaries removed."

docker-build:
	@echo "Building Docker image: $(DOCKER_TAG)"
	docker buildx build --platform linux/amd64,linux/arm64 --tag $(DOCKER_TAG) .

docker-run:
	@echo "Running Docker container: $(DOCKER_TAG)"
	docker run --rm -p 8080:8080 -v ./.env:/app/.env $(DOCKER_TAG)

docker-push:
	@echo "Pushing Docker image: $(DOCKER_TAG)"
	docker push $(DOCKER_TAG)
