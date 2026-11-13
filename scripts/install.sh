#!/usr/bin/env bash
set -euo pipefail

# Simple Server Control (ssc) - 1-Line Ubuntu LTS Installer
# Supported: Ubuntu 22.04 LTS (jammy), 24.04 LTS (noble), 26.04 LTS (resolute) on amd64 / arm64

REPO="${SSC_REPO:-gachon-star-want/simple-server-control}"
INSTALL_BIN="/usr/local/bin/ssc"
DATA_DIR="${SSC_DATA_DIR:-/var/lib/ssc}"
SERVICE_NAME="simple-server-control"
export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a

log()  { printf "\033[1;34m[ssc]\033[0m %s\n" "$*"; }
warn() { printf "\033[1;33m[주의]\033[0m %s\n" "$*"; }
err()  { printf "\033[1;31m[오류]\033[0m %s\n" "$*" >&2; }

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  err "이 설치 스크립트는 root 권한(sudo)으로 실행해야 합니다."
  echo "예시: curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install.sh | sudo bash"
  exit 1
fi

if [[ ! -r /etc/os-release ]]; then
  err "/etc/os-release 파일을 읽을 수 없습니다. Ubuntu 서버인지 확인해 주세요."
  exit 1
fi
# shellcheck disable=SC1091
source /etc/os-release

if [[ "${ID:-}" != "ubuntu" ]]; then
  err "지원하지 않는 운영체제입니다 (${ID:-unknown}). Ubuntu 22.04 / 24.04 / 26.04 LTS만 지원합니다."
  exit 1
fi

case "${VERSION_ID:-}" in
  22.04|24.04|26.04)
    log "Ubuntu ${VERSION_ID} LTS (${VERSION_CODENAME:-unknown}) 확인 완료"
    ;;
  *)
    warn "테스트된 LTS 버전(22.04, 24.04, 26.04)과 다른 Ubuntu ${VERSION_ID:-unknown} 버전입니다. 계속 진행합니다."
    ;;
esac

RAW_ARCH="$(uname -m)"
case "$RAW_ARCH" in
  x86_64|amd64)  ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *)
    err "지원하지 않는 CPU 아키텍처입니다: ${RAW_ARCH} (amd64 / arm64만 지원)"
    exit 1
    ;;
esac

if command -v snap >/dev/null 2>&1 && snap list docker >/dev/null 2>&1; then
  err "Snap 버전 Docker가 설치되어 있어 충돌이 발생할 수 있습니다."
  echo "  아래 명령어로 Snap Docker를 제거한 뒤 이 스크립트를 다시 실행해 주세요:"
  echo "    sudo snap remove --purge docker"
  exit 1
fi

log "1/6 필수 패키지 설치 (curl, git, ca-certificates, gnupg, openssl)..."
apt-get update -qq
apt-get install -y -qq ca-certificates curl git gnupg lsb-release openssl >/dev/null

# Ensure Docker Engine (Official CE repository)
if ! command -v docker >/dev/null 2>&1; then
  log "2/6 공식 Docker CE 설치 중..."
  install -m 0755 -d /etc/apt/keyrings
  if [[ ! -f /etc/apt/keyrings/docker.gpg ]]; then
    curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
    chmod a+r /etc/apt/keyrings/docker.gpg
  fi

  CODENAME="${VERSION_CODENAME:-noble}"
  # Probe Docker apt repo codename; fall back to noble if a new LTS codename is rolling out.
  if ! curl -fsSLI "https://download.docker.com/linux/ubuntu/dists/${CODENAME}/Release" >/dev/null 2>&1; then
    warn "Docker apt 저장소에서 '${CODENAME}' 인덱스를 아직 찾지 못해 'noble' 저장소로 대체합니다."
    CODENAME="noble"
  fi

  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${CODENAME} stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin >/dev/null
else
  log "2/6 Docker가 이미 설치되어 있습니다 ($(docker --version | head -n1))"
fi

# Configure Docker daemon IP address pools & log rotation safely
log "3/6 Docker 네트워크 대역 풀(다중 프로젝트 격리) 및 로그 제한 설정..."
mkdir -p /etc/docker
if [[ ! -f /etc/docker/daemon.json ]]; then
  cat >/etc/docker/daemon.json <<'EOF'
{
  "default-address-pools": [
    { "base": "10.0.0.0/8", "size": 24 }
  ],
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "10m",
    "max-file": "3"
  }
}
EOF
  systemctl restart docker
else
  if ! grep -q "default-address-pools" /etc/docker/daemon.json; then
    warn "/etc/docker/daemon.json 이 이미 존재하여 덮어쓰지 않았습니다. default-address-pools 설정을 권장합니다."
  fi
fi
systemctl enable --now docker >/dev/null 2>&1 || true

# Auto swap setup for low-RAM VPS (<= 2.5 GB) so Railpack/Docker builds never OOM
MEM_MB="$(awk '/MemTotal:/ {printf "%d", $2/1024}' /proc/meminfo)"
SWAP_MB="$(awk '/SwapTotal:/ {printf "%d", $2/1024}' /proc/meminfo)"
if (( MEM_MB <= 2600 && SWAP_MB < 512 )) && [[ ! -f /swapfile ]]; then
  log "저사양 VPS 감지됨 (RAM ${MEM_MB}MB) — 2GB Swap 메모리 생성 중..."
  fallocate -l 2G /swapfile || dd if=/dev/zero of=/swapfile bs=1M count=2048 status=none
  chmod 600 /swapfile
  mkswap /swapfile >/dev/null
  swapon /swapfile
  if ! grep -q "^/swapfile " /etc/fstab; then
    echo "/swapfile none swap sw 0 0" >> /etc/fstab
  fi
fi

# Install Railpack CLI for zero-config Dockerfile-less builds
log "4/6 Railpack 자동 빌드 CLI 설치 확인 중..."
if ! command -v railpack >/dev/null 2>&1; then
  if ! curl -fsSL https://railpack.com/install.sh | sh >/dev/null 2>&1; then
    warn "Railpack 자동 설치에 실패했습니다. Dockerfile 기반 프로젝트는 정상 작동하며, 추후 railpack을 별도 설치할 수 있습니다."
  fi
fi

# Check ports 80/443 warnings
for p in 80 443; do
  if ss -tulpn 2>/dev/null | grep -qE "[:.]${p}[[:space:]]" && ! docker ps --format '{{.Names}}' 2>/dev/null | grep -q '^ssc-caddy$'; then
    warn "포트 ${p}이(가) 이미 다른 프로세스(nginx/apache 등)에 의해 사용 중입니다. Caddy HTTPS 프록시를 위해 해당 웹 서버를 중지하는 것을 권장합니다."
  fi
done

log "5/6 Simple Server Control (${ARCH}) 바이너리 다운로드 중..."
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

BIN_URL="https://github.com/${REPO}/releases/latest/download/simple-server-control-linux-${ARCH}"
if curl -fsSL "$BIN_URL" -o "${TMP_DIR}/ssc"; then
  SUM_URL="https://github.com/${REPO}/releases/latest/download/checksums.txt"
  if curl -fsSL "$SUM_URL" -o "${TMP_DIR}/checksums.txt" 2>/dev/null; then
    EXPECTED="$(grep "simple-server-control-linux-${ARCH}" "${TMP_DIR}/checksums.txt" | awk '{print $1}' | head -n1 || true)"
    if [[ -n "$EXPECTED" ]]; then
      ACTUAL="$(sha256sum "${TMP_DIR}/ssc" | awk '{print $1}')"
      if [[ "$EXPECTED" != "$ACTUAL" ]]; then
        err "다운로드한 바이너리 SHA-256 체크섬이 일치하지 않습니다!"
        exit 1
      fi
    fi
  fi
  install -m 0755 "${TMP_DIR}/ssc" "$INSTALL_BIN"
else
  err "GitHub Releases에서 바이너리를 다운로드하지 못했습니다: ${BIN_URL}"
  echo "  아직 첫 Release가 게시되지 않았다면 소스에서 'make build'로 빌드 후 /usr/local/bin/ssc에 배치할 수 있습니다."
  exit 1
fi

log "6/6 systemd 서비스 등록 및 초기 Setup Token 생성 중..."
mkdir -p "$DATA_DIR"
chmod 700 "$DATA_DIR"
if [[ ! -f "${DATA_DIR}/setup.token" ]]; then
  openssl rand -hex 16 > "${DATA_DIR}/setup.token"
  chmod 600 "${DATA_DIR}/setup.token"
fi

cat >"/etc/systemd/system/${SERVICE_NAME}.service" <<EOF
[Unit]
Description=Simple Server Control (ssc) - Lightweight Korean Self-Hosted PaaS
Documentation=https://github.com/${REPO}
After=network-online.target docker.service
Requires=docker.service
Wants=network-online.target

[Service]
Type=simple
ExecStart=${INSTALL_BIN} serve
Environment=SSC_DATA_DIR=${DATA_DIR}
Environment=SSC_LISTEN_ADDR=:8080
WorkingDirectory=${DATA_DIR}
Restart=always
RestartSec=3
TimeoutStopSec=30
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now "${SERVICE_NAME}"

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  log "활성화된 UFW 방화벽에 80, 443, 8080 포트를 허용합니다..."
  ufw allow 80/tcp >/dev/null || true
  ufw allow 443/tcp >/dev/null || true
  ufw allow 8080/tcp >/dev/null || true
fi

PUBLIC_IP="$(curl -fsSL --max-time 3 https://api4.ipify.org 2>/dev/null || hostname -I | awk '{print $1}')"
SETUP_TOKEN="$(cat "${DATA_DIR}/setup.token")"

echo ""
echo "====================================================================="
echo "  Simple Server Control 설치가 완료되었습니다!"
echo "---------------------------------------------------------------------"
echo "  대시보드 접속 : http://${PUBLIC_IP}:8080"
echo "  Setup Token   : ${SETUP_TOKEN}"
echo "  데이터 경로   : ${DATA_DIR}"
echo "  상태 진단 CLI : ssc doctor"
echo "====================================================================="
echo ""
