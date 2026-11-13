#!/usr/bin/env bash
set -euo pipefail

# Simple Server Control (ssc) Uninstaller
# Pass --purge to also remove /var/lib/ssc (database, encrypted secrets, logs).

SERVICE_NAME="simple-server-control"
INSTALL_BIN="/usr/local/bin/ssc"
DATA_DIR="${SSC_DATA_DIR:-/var/lib/ssc}"

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "root 권한(sudo)으로 실행해 주세요." >&2
  exit 1
fi

echo "[ssc] systemd 서비스 중지 및 제거 중..."
systemctl disable --now "${SERVICE_NAME}" >/dev/null 2>&1 || true
rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
systemctl daemon-reload >/dev/null 2>&1 || true

echo "[ssc] 바이너리 제거: ${INSTALL_BIN}"
rm -f "${INSTALL_BIN}"

if [[ "${1:-}" == "--purge" ]]; then
  echo "[ssc] --purge 옵션: 데이터 디렉터리 및 관리 컨테이너 정리 중..."
  docker rm -f ssc-caddy ssc-buildkit >/dev/null 2>&1 || true
  rm -rf "${DATA_DIR}"
  echo "[ssc] 모든 데이터가 삭제되었습니다."
else
  echo "[ssc] 데이터 디렉터리(${DATA_DIR})는 보존되었습니다. 완전 삭제하려면 --purge 옵션을 주세요."
fi
