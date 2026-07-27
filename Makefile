# Makefile for MyMCP Server

BINARY_NAME=mymcp
BUILD_DIR=build
DIST_DIR=dist
VERSION?=v0.1.0-beta

.PHONY: all build test clean build-all package

all: test build

build:
	go build -o $(BINARY_NAME) .

test:
	go test -v ./...

build-linux-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-amd64 .

build-linux-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-linux-arm64 .

build-windows-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=windows GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe .

build-darwin-amd64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=amd64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-amd64 .

build-darwin-arm64:
	@mkdir -p $(BUILD_DIR)
	GOOS=darwin GOARCH=arm64 go build -o $(BUILD_DIR)/$(BINARY_NAME)-darwin-arm64 .

build-all: build-linux-amd64 build-linux-arm64 build-windows-amd64 build-darwin-amd64 build-darwin-arm64

package: build-all
	@mkdir -p $(DIST_DIR)
	tar -czvf $(DIST_DIR)/$(BINARY_NAME)-linux-amd64-$(VERSION).tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-linux-amd64
	tar -czvf $(DIST_DIR)/$(BINARY_NAME)-linux-arm64-$(VERSION).tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-linux-arm64
	zip -j $(DIST_DIR)/$(BINARY_NAME)-windows-amd64-$(VERSION).zip $(BUILD_DIR)/$(BINARY_NAME)-windows-amd64.exe
	tar -czvf $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64-$(VERSION).tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-darwin-amd64
	tar -czvf $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64-$(VERSION).tar.gz -C $(BUILD_DIR) $(BINARY_NAME)-darwin-arm64
	@echo "Package creation complete in $(DIST_DIR)/"

clean:
	rm -rf $(BUILD_DIR) $(DIST_DIR) $(BINARY_NAME) $(BINARY_NAME).exe
