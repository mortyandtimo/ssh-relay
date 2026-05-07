#!/usr/bin/env bash
#===============================================================================
# SSH Relay - One-command installer
# Usage: curl -fsSL https://raw.githubusercontent.com/<user>/<repo>/main/install.sh | bash
#===============================================================================
set -euo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'

banner(){
  echo -e "${CYAN}"
  echo "  ╔══════════════════════════════════════════╗"
  echo "  ║         SSH Relay Installer             ║"
  echo "  ║     Reverse SSH Tunnel Manager          ║"
  echo "  ╚══════════════════════════════════════════╝"
  echo -e "${NC}"
}

banner

REPO="https://github.com/mortyandtimo/ssh-relay"
RELEASES="$REPO/releases/download"

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *) echo "unsupported arch: $(uname -m)" >&2; exit 1 ;;
  esac
}

detect_os() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    *) echo "unsupported os: $(uname -s)" >&2; exit 1 ;;
  esac
}

ARCH=$(detect_arch)
OS=$(detect_os)

echo ""
echo -e "${CYAN}Platform:${NC} $OS/$ARCH"
echo ""
echo -e "Choose install mode:"
echo "  1) Server  (install ssh-relay-api on cloud VM)"
echo "  2) Client  (install sshr on this machine)"
echo -n "Select [1/2]: "
read MODE

case "$MODE" in
  1)
    echo ""
    echo -e "${CYAN}Installing server (ssh-relay-api)...${NC}"

    # Determine latest version tag or use main
    VERSION="${SSHR_VERSION:-latest}"
    BIN_URL="$RELEASES/$VERSION/ssh-relay-api-$OS-$ARCH"
    SCRIPT_URL="$REPO/raw/main/server-install.sh"

    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    echo "Downloading ssh-relay-api..."
    curl -fsSL "$BIN_URL" -o "$TMPDIR/ssh-relay-api" || {
      echo "Binary not found at $BIN_URL - build from source: git clone $REPO && cd cloud-relay-platform && go build -o ssh-relay-api ./apps/ssh-relay-api/cmd/ssh-relay-api/"
      exit 1
    }
    chmod +x "$TMPDIR/ssh-relay-api"

    echo "Downloading server-install.sh..."
    curl -fsSL "$SCRIPT_URL" -o "$TMPDIR/server-install.sh" 2>/dev/null || true

    if [[ -f "$TMPDIR/server-install.sh" ]]; then
      chmod +x "$TMPDIR/server-install.sh"
      cd "$TMPDIR"
      exec bash server-install.sh
    else
      echo ""
      echo -e "${YELLOW}Manual install steps:${NC}"
      echo "  1. sudo mv $TMPDIR/ssh-relay-api /opt/cloud-relay-platform/bin/"
      echo "  2. Create /etc/cloud-relay-platform/ssh-relay-api.env (see env example)"
      echo "  3. Install and start systemd service"
      echo "  See README for details: $REPO"
    fi
    ;;

  2)
    echo ""
    echo -e "${CYAN}Installing client (sshr)...${NC}"

    VERSION="${SSHR_VERSION:-latest}"
    BIN_URL="$RELEASES/$VERSION/sshr-$OS-$ARCH"

    TMPDIR=$(mktemp -d)
    trap "rm -rf $TMPDIR" EXIT

    echo "Downloading sshr..."
    curl -fsSL "$BIN_URL" -o "$TMPDIR/sshr" 2>/dev/null || {
      echo "Binary not found, trying source build..."
      echo "git clone $REPO && cd cloud-relay-platform && go build -o sshr ./apps/ssh-relay-cli/cmd/sshr/"
      exit 1
    }
    chmod +x "$TMPDIR/sshr"
    sudo install -m 0755 "$TMPDIR/sshr" /usr/local/bin/sshr
    echo -e "${GREEN}sshr installed to /usr/local/bin/sshr${NC}"

    echo ""
    echo -e "${CYAN}Next steps:${NC}"
    echo "  1. Set your relay server: export SSHR_SERVER=https://tunnel.example.com"
    echo "  2. Register: sshr register"
    echo "  3. Create forward: sshr forward"
    echo "  4. Start daemon: sshr daemon"
    echo ""
    echo "  Or run the guided client installer:"
    echo "  curl -fsSL $REPO/raw/main/client-install.sh | bash"
    ;;

  *)
    echo "Invalid choice" >&2; exit 1 ;;
esac
