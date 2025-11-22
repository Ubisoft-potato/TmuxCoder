# TmuxCoder Makefile

.PHONY: all build install uninstall clean test help

# Variables
BINARY_NAME=tmuxcoder
INSTALL_PATH=/usr/local/bin
GO=go
GOFLAGS=-ldflags="-s -w"
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# Build output
BUILD_DIR=build
DIST_DIR=cmd/$(BINARY_NAME)/dist

# Colors for output
GREEN=\033[0;32m
YELLOW=\033[0;33m
RED=\033[0;31m
NC=\033[0m # No Color

all: build ## Build all binaries (default target)

help: ## Show this help message
	@echo "$(GREEN)TmuxCoder Build System$(NC)"
	@echo ""
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  $(YELLOW)%-15s$(NC) %s\n", $$1, $$2}'

build: build-cli build-panels ## Build CLI and all panel binaries

build-cli: ## Build the tmuxcoder CLI binary
	@echo "$(GREEN)Building tmuxcoder CLI...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/$(BINARY_NAME)
	@echo "$(GREEN)✓ Built: $(BUILD_DIR)/$(BINARY_NAME)$(NC)"
	@# Also create a copy in project root for convenience
	@cp $(BUILD_DIR)/$(BINARY_NAME) ./$(BINARY_NAME)
	@echo "$(GREEN)✓ Copied to: ./$(BINARY_NAME)$(NC)"

build-panels: ## Build all panel binaries
	@echo "$(GREEN)Building panel binaries...$(NC)"
	@for pkg in cmd/opencode-tmux cmd/opencode-sessions cmd/opencode-messages cmd/opencode-input; do \
		out="$$pkg/dist/$$(basename "$$pkg" | sed 's/opencode-//')-pane"; \
		if [ "$$pkg" = "cmd/opencode-tmux" ]; then \
			out="$$pkg/dist/opencode-tmux"; \
		fi; \
		mkdir -p "$$(dirname "$$out")"; \
		echo "  -> Building $$pkg"; \
		$(GO) build $(GOFLAGS) -o "$$out" "./$$pkg" || exit 1; \
	done
	@echo "$(GREEN)✓ All panels built$(NC)"

install: build ## Install tmuxcoder to system (requires sudo)
	@echo "$(GREEN)Installing $(BINARY_NAME) to $(INSTALL_PATH)...$(NC)"
	@if [ ! -w $(INSTALL_PATH) ]; then \
		echo "$(YELLOW)Note: $(INSTALL_PATH) requires sudo access$(NC)"; \
		sudo install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME); \
	else \
		install -m 755 $(BUILD_DIR)/$(BINARY_NAME) $(INSTALL_PATH)/$(BINARY_NAME); \
	fi
	@echo "$(GREEN)✓ Installed: $(INSTALL_PATH)/$(BINARY_NAME)$(NC)"
	@echo "$(GREEN)You can now run: $(BINARY_NAME)$(NC)"

install-user: build ## Install to ~/bin (no sudo required)
	@echo "$(GREEN)Installing $(BINARY_NAME) to ~/bin...$(NC)"
	@mkdir -p ~/bin
	@install -m 755 $(BUILD_DIR)/$(BINARY_NAME) ~/bin/$(BINARY_NAME)
	@echo "$(GREEN)✓ Installed: ~/bin/$(BINARY_NAME)$(NC)"
	@if echo $$PATH | grep -q "$$HOME/bin"; then \
		echo "$(GREEN)You can now run: $(BINARY_NAME)$(NC)"; \
	else \
		echo "$(YELLOW)Warning: ~/bin is not in your PATH$(NC)"; \
		echo "$(YELLOW)Add this to your ~/.bashrc or ~/.zshrc:$(NC)"; \
		echo "  export PATH=\"\$$HOME/bin:\$$PATH\""; \
	fi

uninstall: ## Uninstall tmuxcoder from system
	@echo "$(YELLOW)Uninstalling $(BINARY_NAME)...$(NC)"
	@if [ -f $(INSTALL_PATH)/$(BINARY_NAME) ]; then \
		if [ ! -w $(INSTALL_PATH) ]; then \
			sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME); \
		else \
			rm -f $(INSTALL_PATH)/$(BINARY_NAME); \
		fi; \
		echo "$(GREEN)✓ Uninstalled from $(INSTALL_PATH)$(NC)"; \
	fi
	@if [ -f ~/bin/$(BINARY_NAME) ]; then \
		rm -f ~/bin/$(BINARY_NAME); \
		echo "$(GREEN)✓ Uninstalled from ~/bin$(NC)"; \
	fi

clean: ## Clean build artifacts
	@echo "$(YELLOW)Cleaning build artifacts...$(NC)"
	@rm -rf $(BUILD_DIR)
	@rm -f ./$(BINARY_NAME)
	@rm -f cmd/opencode-tmux/dist/opencode-tmux
	@rm -f cmd/opencode-sessions/dist/sessions-pane
	@rm -f cmd/opencode-messages/dist/messages-pane
	@rm -f cmd/opencode-input/dist/input-pane
	@echo "$(GREEN)✓ Cleaned$(NC)"

test: ## Run tests
	@echo "$(GREEN)Running tests...$(NC)"
	@$(GO) test -v ./...

deps: ## Download dependencies
	@echo "$(GREEN)Downloading Go dependencies...$(NC)"
	@$(GO) mod download
	@echo "$(GREEN)✓ Dependencies downloaded$(NC)"

setup-opencode: ## Setup opencode submodule and install dependencies
	@echo "$(GREEN)Setting up opencode submodule...$(NC)"
	@git submodule update --init --recursive
	@if [ -d "packages/opencode" ] && [ ! -d "packages/opencode/node_modules" ]; then \
		echo "$(GREEN)Installing opencode dependencies...$(NC)"; \
		cd packages/opencode && bun install; \
	fi
	@echo "$(GREEN)✓ OpenCode setup complete$(NC)"

dev: build-panels ## Quick build for development (panels only)
	@echo "$(GREEN)✓ Development build complete$(NC)"

release: ## Build release version with version info
	@echo "$(GREEN)Building release version $(VERSION)...$(NC)"
	@mkdir -p $(BUILD_DIR)
	@$(GO) build -ldflags="-s -w -X main.version=$(VERSION)" -o $(BUILD_DIR)/$(BINARY_NAME) ./cmd/$(BINARY_NAME)
	@echo "$(GREEN)✓ Release built: $(BUILD_DIR)/$(BINARY_NAME) (v$(VERSION))$(NC)"

.DEFAULT_GOAL := help
