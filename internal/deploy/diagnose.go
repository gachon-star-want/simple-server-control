// Package deploy implements database provisioning, 6-stage zero-downtime app rollouts,
// live deployment log broadcasting, and Korean error diagnosis for non-developer operators.
package deploy

import (
	"fmt"
	"strings"
)

// Diagnosis translates raw technical failure output into actionable Korean guidance.
type Diagnosis struct {
	Code     string
	TitleKo  string
	DetailKo string
}

// DiagnoseFailure classifies raw pipeline errors + recent log tail into clear Korean messages.
func DiagnoseFailure(stage string, err error, logTail string) Diagnosis {
	raw := ""
	if err != nil {
		raw += strings.ToLower(err.Error()) + "\n"
	}
	raw += strings.ToLower(logTail)

	switch {
	case strings.Contains(raw, "killed") || strings.Contains(raw, "oom") || strings.Contains(raw, "out of memory") || strings.Contains(raw, "cannot allocate memory"):
		return Diagnosis{
			Code:     "BUILD_OOM",
			TitleKo:  "서버 메모리가 부족해 작업이 중단되었습니다 (OOM)",
			DetailKo: "빌드 또는 실행 중 RAM이 부족했습니다. 스왑 메모리가 활성화되어 있는지 `ssc doctor`로 확인하거나, 동시에 무거운 빌드를 실행하지 말고 재시도해 주세요.",
		}
	case stage == "cloning" || strings.Contains(raw, "authentication failed") || strings.Contains(raw, "could not read username") || strings.Contains(raw, "repository not found"):
		return Diagnosis{
			Code:     "GIT_AUTH_FAILED",
			TitleKo:  "GitHub 저장소에서 코드를 가져오지 못했습니다",
			DetailKo: "GitHub App에 해당 저장소 권한이 연결되어 있는지, 그리고 설정된 브랜치 이름(예: main)이 실제 저장소와 일치하는지 확인해 주세요.",
		}
	case strings.Contains(raw, "missing lockfile") || strings.Contains(raw, "npm err") || strings.Contains(raw, "enoent") || strings.Contains(raw, "mod tidy") || strings.Contains(raw, "pip "):
		return Diagnosis{
			Code:     "BUILD_DEPENDENCY_ERROR",
			TitleKo:  "패키지/의존성 설치 단계에서 빌드가 실패했습니다",
			DetailKo: "package-lock.json, requirements.txt 또는 go.mod 파일이 지정한 루트 디렉터리(Root Directory) 안에 포함되어 있는지 확인하고 로그 마지막 줄을 살펴봐 주세요.",
		}
	case strings.Contains(raw, "railpack binary missing"):
		return Diagnosis{
			Code:     "RAILPACK_MISSING",
			TitleKo:  "Railpack 빌더가 서버에 설치되어 있지 않습니다",
			DetailKo: "저장소에 Dockerfile을 추가하거나 서버에 railpack CLI가 설치되어 있는지 `ssc doctor`로 점검해 주세요.",
		}
	case stage == "building":
		return Diagnosis{
			Code:     "BUILD_FAILED",
			TitleKo:  "애플리케이션 이미지 빌드에 실패했습니다",
			DetailKo: "소스 코드 문법 오류나 Dockerfile/Root Directory 경로 설정 문제일 가능성이 큽니다. 아래 배포 로그 마지막 에러 메시지를 확인해 주세요.",
		}
	case strings.Contains(raw, "healthcheck_404"):
		return Diagnosis{
			Code:     "HEALTHCHECK_404",
			TitleKo:  "새 컨테이너가 부팅됐지만 헬스체크 경로가 404를 반환했습니다",
			DetailKo: "서비스 설정의 헬스체크 경로(현재 /health 등)를 앱에 실제 존재하는 경로(예: /)로 수정한 뒤 다시 배포해 주세요. 기존 서비스는 중단 없이 유지되었습니다.",
		}
	case stage == "healthchecking":
		return Diagnosis{
			Code:     "HEALTHCHECK_TIMEOUT",
			TitleKo:  "새 컨테이너 응답 확인(헬스체크) 시간 초과 — 기존 서비스 유지됨",
			DetailKo: fmt.Sprintf("앱이 지정한 포트(PORT)로 웹 요청을 받지 못했거나 부팅 중 종료됐습니다. 서비스 설정의 포트/시크릿(예: DATABASE_URL)과 컨테이너 로그를 확인해 주세요."),
		}
	case stage == "switching":
		return Diagnosis{
			Code:     "PROXY_SWITCH_FAILED",
			TitleKo:  "도메인 프록시(Caddy) 트래픽 전환에 실패했습니다",
			DetailKo: "ssc-caddy 컨테이너 상태가 정상인지 확인해 주세요. 기존 버전 컨테이너로 안전하게 유지되었습니다.",
		}
	default:
		msg := "알 수 없는 오류가 발생했습니다."
		if err != nil {
			msg = fmt.Sprintf("상세 오류: %v", err)
		}
		return Diagnosis{
			Code:     "DEPLOY_FAILED",
			TitleKo:  "배포 중 오류가 발생해 이전 버전을 유지했습니다",
			DetailKo: msg,
		}
	}
}
