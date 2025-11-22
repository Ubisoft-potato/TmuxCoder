#!/usr/bin/env bash
#
# TmuxCoder Installation Script
# This script builds and installs tmuxcoder binary and sets up dependencies
#

set -euo pipefail

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Script directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Check if running as root
if [[ $EUID -eq 0 ]]; then
   echo -e "${RED}Error: Do not run this script as root${NC}"
   exit 1
fi

# Print colored output
print_step() {
    echo -e "${BLUE}==>${NC} ${GREEN}$1${NC}"
}

print_warning() {
    echo -e "${YELLOW}Warning:${NC} $1"
}

print_error() {
    echo -e "${RED}Error:${NC} $1"
}

print_success() {
    echo -e "${GREEN}✓${NC} $1"
}

# Check dependencies
check_dependencies() {
    print_step "Checking dependencies..."

    local missing_deps=()

    # Check Go
    if ! command -v go &> /dev/null; then
        missing_deps+=("go (>= 1.24)")
    else
        GO_VERSION=$(go version | awk '{print $3}' | sed 's/go//')
        print_success "Go $GO_VERSION found"
    fi

    # Check tmux
    if ! command -v tmux &> /dev/null; then
        missing_deps+=("tmux (>= 3.2)")
    else
        TMUX_VERSION=$(tmux -V | awk '{print $2}')
        print_success "tmux $TMUX_VERSION found"
    fi

    # Check bun (for opencode)
    if ! command -v bun &> /dev/null; then
        print_warning "bun not found - required for opencode server"
        echo "  Install from: https://bun.sh"
        missing_deps+=("bun")
    else
        BUN_VERSION=$(bun --version)
        print_success "bun $BUN_VERSION found"
    fi

    if [[ ${#missing_deps[@]} -gt 0 ]]; then
        print_error "Missing required dependencies:"
        for dep in "${missing_deps[@]}"; do
            echo "  - $dep"
        done
        exit 1
    fi
}

# Setup opencode submodule
setup_opencode() {
    print_step "Setting up opencode submodule..."

    cd "$SCRIPT_DIR"

    # Initialize submodule if needed
    if [[ ! -f "packages/opencode/.git" ]]; then
        print_step "Initializing git submodule..."
        git submodule update --init --recursive
    fi

    # Install opencode dependencies
    if [[ -d "packages/opencode" && ! -d "packages/opencode/node_modules" ]]; then
        print_step "Installing opencode dependencies..."
        cd packages/opencode/packages/opencode
        bun install || {
            print_warning "bun install failed, you may need to fix permissions"
            print_warning "Try running: sudo chown -R \$(whoami):staff ~/.npm ~/.bun"
            return 1
        }
        cd "$SCRIPT_DIR"
    fi

    print_success "OpenCode setup complete"
}

# Build binaries
build_binaries() {
    print_step "Building binaries..."

    cd "$SCRIPT_DIR"

    if command -v make &> /dev/null; then
        make build || {
            print_error "Build failed"
            exit 1
        }
    else
        # Fallback to manual build
        print_warning "make not found, using manual build"

        # Build CLI
        mkdir -p build
        go build -ldflags="-s -w" -o build/tmuxcoder ./cmd/tmuxcoder

        # Build panels
        for pkg in cmd/opencode-tmux cmd/opencode-sessions cmd/opencode-messages cmd/opencode-input; do
            out="$pkg/dist/$(basename "$pkg" | sed 's/opencode-//')-pane"
            [[ "$pkg" == "cmd/opencode-tmux" ]] && out="$pkg/dist/opencode-tmux"
            mkdir -p "$(dirname "$out")"
            echo "  -> Building $pkg"
            go build -ldflags="-s -w" -o "$out" "./$pkg"
        done
    fi

    print_success "Build complete"
}

# Install binary
install_binary() {
    print_step "Installing tmuxcoder..."

    local install_method=""

    # Ask user where to install
    echo ""
    echo "Choose installation location:"
    echo "  1) /usr/local/bin (system-wide, requires sudo)"
    echo "  2) ~/bin (user-only, no sudo)"
    echo "  3) Skip installation (build only)"
    echo ""

    while true; do
        read -p "Select [1-3]: " choice
        case $choice in
            1)
                install_method="system"
                break
                ;;
            2)
                install_method="user"
                break
                ;;
            3)
                install_method="skip"
                break
                ;;
            *)
                echo "Invalid choice, please select 1, 2, or 3"
                ;;
        esac
    done

    cd "$SCRIPT_DIR"

    case $install_method in
        system)
            if command -v make &> /dev/null; then
                make install
            else
                sudo install -m 755 build/tmuxcoder /usr/local/bin/tmuxcoder
            fi
            print_success "Installed to /usr/local/bin/tmuxcoder"
            ;;
        user)
            mkdir -p ~/bin
            install -m 755 build/tmuxcoder ~/bin/tmuxcoder
            print_success "Installed to ~/bin/tmuxcoder"

            if ! echo "$PATH" | grep -q "$HOME/bin"; then
                print_warning "~/bin is not in your PATH"
                echo ""
                echo "Add this to your ~/.bashrc or ~/.zshrc:"
                echo -e "  ${BLUE}export PATH=\"\$HOME/bin:\$PATH\"${NC}"
                echo ""
            fi
            ;;
        skip)
            print_success "Binary built at: build/tmuxcoder"
            echo "You can manually copy it to your desired location"
            ;;
    esac
}

# Create default config
create_default_config() {
    print_step "Creating default configuration..."

    local config_dir="$HOME/.opencode"
    mkdir -p "$config_dir"

    if [[ ! -f "$config_dir/tmux.yaml" ]]; then
        cat > "$config_dir/tmux.yaml" <<'EOF'
version: "1.0"
mode: raw
session:
  name: tmux-coder
panels:
  - id: sessions
    type: sessions
    width: "22%"
  - id: messages
    type: messages
  - id: input
    type: input
    height: "25%"
splits:
  - type: horizontal
    target: root
    panels: ["sessions", "messages"]
    ratio: "1:2"
  - type: vertical
    target: messages
    panels: ["messages", "input"]
    ratio: "3:1"
EOF
        print_success "Created default config at $config_dir/tmux.yaml"
    else
        print_success "Config already exists at $config_dir/tmux.yaml"
    fi
}

# Print next steps
print_next_steps() {
    echo ""
    echo -e "${GREEN}======================================${NC}"
    echo -e "${GREEN}Installation Complete!${NC}"
    echo -e "${GREEN}======================================${NC}"
    echo ""
    echo "Next steps:"
    echo ""
    echo "1. Start tmuxcoder:"
    echo -e "   ${BLUE}./tmuxcoder${NC}"
    echo ""
    echo "2. Or use the start script directly:"
    echo -e "   ${BLUE}./scripts/start.sh${NC}"
    echo ""
    echo "3. View help:"
    echo -e "   ${BLUE}tmuxcoder --help${NC}"
    echo ""
    echo "Configuration:"
    echo "  - Config: ~/.opencode/tmux.yaml"
    echo "  - State:  ~/.opencode/state.json"
    echo "  - Logs:   ~/.opencode/*.log"
    echo ""
    echo "For more information, see README.md"
    echo ""
}

# Main installation flow
main() {
    echo ""
    echo -e "${GREEN}╔═══════════════════════════════════╗${NC}"
    echo -e "${GREEN}║   TmuxCoder Installation Script  ║${NC}"
    echo -e "${GREEN}╚═══════════════════════════════════╝${NC}"
    echo ""

    check_dependencies

    # Ask if user wants to setup opencode
    echo ""
    read -p "Setup opencode submodule and dependencies? [Y/n]: " -n 1 -r
    echo
    if [[ $REPLY =~ ^[Yy]$ ]] || [[ -z $REPLY ]]; then
        setup_opencode || print_warning "OpenCode setup had issues, but continuing..."
    fi

    build_binaries
    install_binary
    create_default_config
    print_next_steps
}

# Run main
main
