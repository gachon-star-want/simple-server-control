# Simple Server Control (`ssc`)

**내 Ubuntu VPS 1대에 Railway 같은 다중 프로젝트 캔버스 · 무중단 GitHub 자동 배포 · 시크릿 암호화 관리 · 다중 도메인 HTTPS를 한 줄로 설치하는 초경량 한국어 셀프호스팅 PaaS**

---

## 주요 기능

- **다중 프로젝트 독립 관리 (Multi-Project Canvas)**
  - `pleumrouter` 를 비롯한 여러 프로덕트/서비스를 프로젝트 단위로 완전히 분리
  - 프로젝트마다 전용 격리 Docker 네트워크(`ssc-proj-<slug>`) 자동 생성
  - **Railway 스타일 인터랙티브 캔버스**: 인터넷 진입점(Caddy) ↔ 앱 컨테이너 ↔ 내부 DB 연결선, 실행 컨테이너 수, 시크릿 변경 배지를 시각적으로 확인
- **직관적인 시크릿(환경 변수) 관리**
  - SQLite 저장 전 **AES-256-GCM 암호화** (`master.key`)
  - 기본 마스킹 + **Sealed Secret**(저장 후 읽기 잠금), **`.env` 통째로 붙여넣기**, 공유 환경 변수, `${{postgres.DATABASE_URL}}` 서비스 간 참조
  - 시크릿 변경 시 실행 중인 컨테이너에 즉시 반영 여부를 알려주는 **재배포 알림**
- **GitHub 1클릭 연동 & 6단계 무중단 배포**
  - GitHub App Manifest 브라우저 원클릭 연동 (개인/조직 저장소, Push Webhook `X-Hub-Signature-256` 검증)
  - Dockerfile 자동 감지 또는 **Railpack** 무설정 자동 빌드
  - `코드 준비 → 빌드 → 새 컨테이너 기동 → 헬스체크 → Caddy 무중단 전환 → 구버전 정리`
  - **안전 롤백 보장**: 헬스체크 실패 시 새 컨테이너만 정리하고 기존 라이브 컨테이너는 100% 중단 없이 유지하며 한국어 원인 진단 제공
- **VPS 1대에서 여러 도메인 자동 HTTPS**
  - `ssc-caddy` 내장 리버스 프록시가 프로젝트별 여러 커스텀 도메인에 Let's Encrypt HTTPS 자동 적용
  - 도메인이 없어도 `*.sslip.io` 테스트 URL 자동 발급 + DNS A 레코드 실시간 확인
- **초경량 단일 바이너리 (`CGO_ENABLED=0`)**
  - Go 단일 바이너리 + 내장 React SPA + SQLite WAL (별도 Redis/Postgres 컨트롤 플레인 불필요)

---

## 지원 운영체제

- **Ubuntu 22.04 LTS (`jammy`)** — `amd64`, `arm64`
- **Ubuntu 24.04 LTS (`noble`)** — `amd64`, `arm64`
- **Ubuntu 26.04 LTS (`resolute`)** — `amd64`, `arm64`

---

## Ubuntu VPS 1줄 설치

```bash
curl -fsSL https://raw.githubusercontent.com/gachon-star-want/simple-server-control/main/scripts/install.sh | sudo bash
```

설치 스크립트가 자동으로 처리하는 작업:
1. Snap Docker 충돌 사전 확인 (`snap list docker`)
2. 공식 Docker CE 설치 및 다중 프로젝트용 IP 대역 풀(`10.0.0.0/8`, `/etc/docker/daemon.json`) + 로그 로테이션 설정
3. 저사양 VPS(RAM ≤ 2.5GB)에서 빌드 중 메모리 부족(OOM)을 막기 위한 2GB Swap 자동 생성
4. Railpack / Git 설치 및 바이너리 SHA-256 무결성 검증
5. `systemd` 서비스(`simple-server-control.service`) 등록 및 초기 1회용 `Setup Token` 발급

설치 후 터미널에 출력되는 대시보드 주소(`http://<서버IP>:8080`)로 접속해 Setup Token과 관리자 계정을 등록하면 바로 사용할 수 있습니다.

---

## 주요 CLI 명령어

```bash
ssc doctor                  # Ubuntu LTS, Docker, Caddy, Swap, 디스크 상태 한국어 진단
ssc reset-password          # 관리자 비밀번호 분실 시 CLI로 즉시 초기화
ssc version                 # 설치된 버전 확인
```

제거:

```bash
sudo bash scripts/uninstall.sh           # 서비스/바이너리 제거 (데이터 보존)
sudo bash scripts/uninstall.sh --purge   # /var/lib/ssc 데이터까지 완전 삭제
```

---

## 로컬 개발 & 빌드

필요 환경: Go 1.27+, Node.js 22+

```bash
# 1. 프론트엔드 빌드 + Go 백엔드 전체 테스트 (race detector)
make test

# 2. 현재 OS 바이너리 빌드 -> dist/ssc
make build

# 3. Ubuntu amd64 / arm64 정적 릴리스 바이너리 크로스 컴파일
make release-binaries
```

---

## 라이선스

[MIT License](./LICENSE)
