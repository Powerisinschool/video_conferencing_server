# Project Variables
BINARY_NAME=video_conferencing_server
ENTRY_POINT=./cmd/server/main.go

# Default target: Build the project
all: build

# Install dependencies and tidy go.mod
deps:
	go mod tidy
	go mod download

# Build the binary
# We now point to the new location: cmd/server/main.go
build:
	go build -o $(BINARY_NAME) $(ENTRY_POINT)

# Run the binary (Builds first)
run: build
	./$(BINARY_NAME)

# Development Run (Runs directly without creating a binary file)
# Useful for quick testing during development
dev:
	go run $(ENTRY_POINT)

# Run all tests in the project (recursively)
test:
	go test -v ./...

# Clean up binaries and temporary files
clean:
	go clean
	rm -f $(BINARY_NAME)
