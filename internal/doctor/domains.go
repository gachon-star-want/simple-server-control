package doctor

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gachon-star-want/simple-server-control/internal/proxy"
)

// CheckPanelExposure warns when the panel listens on an address reachable from outside the server.
func CheckPanelExposure(listenAddr string) CheckResult {
	name := "관리 화면 노출"
	host, _, err := net.SplitHostPort(listenAddr)
	ip := net.ParseIP(host)
	// The CLI does not see the systemd unit's env, so also probe the port on a public interface.
	if err == nil && (host == "localhost" || (ip != nil && ip.IsLoopback())) && !reachableOnLAN(listenAddr) {
		return CheckResult{Name: name, Status: "ok", MessageKo: fmt.Sprintf("관리 화면은 서버 안(%s)에서만 열립니다. 밖에서는 HTTPS 도메인 또는 SSH 터널로 접속합니다.", listenAddr)}
	}
	return CheckResult{
		Name:          name,
		Status:        "warn",
		MessageKo:     fmt.Sprintf("관리 화면이 %s 로 열려 있어 인터넷에서 바로 접속을 시도할 수 있습니다.", listenAddr),
		RemediationKo: "/etc/systemd/system/simple-server-control.service 의 SSC_LISTEN_ADDR 를 127.0.0.1:8080 으로 바꾸고 'sudo systemctl daemon-reload && sudo systemctl restart simple-server-control' 후 'sudo ssc panel-domain <도메인>' 으로 접속하세요.",
	}
}

// CheckDomains resolves each routed hostname and probes HTTPS, explaining Cloudflare-specific failures.
func CheckDomains(ctx context.Context, hosts []string) []CheckResult {
	client := &http.Client{
		Timeout:       6 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	out := make([]CheckResult, 0, len(hosts))
	for _, host := range hosts {
		lookupCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		ips, err := net.DefaultResolver.LookupHost(lookupCtx, host)
		cancel()
		if err != nil || len(ips) == 0 {
			out = append(out, CheckResult{Name: "도메인 " + host, Status: "warn",
				MessageKo:     "DNS 조회에 실패했습니다.",
				RemediationKo: "도메인 관리 화면에서 A 레코드가 이 서버 IP를 가리키는지 확인해 주세요."})
			continue
		}
		behindCF := proxy.IsCloudflareIP(ips[0])
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
		status := 0
		resp, err := client.Do(req)
		if err == nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		res := ClassifyHTTPS(status, err, behindCF)
		res.Name = "도메인 " + host
		out = append(out, res)
	}
	return out
}

// ClassifyHTTPS turns one HTTPS probe result into Korean guidance (pure, for testing).
func ClassifyHTTPS(status int, probeErr error, behindCF bool) CheckResult {
	via := "직접 연결"
	if behindCF {
		via = "Cloudflare 프록시"
	}
	switch {
	case behindCF && (status == 525 || status == 526):
		return CheckResult{Status: "fail",
			MessageKo:     fmt.Sprintf("Cloudflare가 서버의 HTTPS 인증서를 확인하지 못했습니다 (오류 %d). 서버 쪽 인증서가 아직 발급되지 않았습니다.", status),
			RemediationKo: proxy.CloudflareGuideKo}
	case behindCF && status >= 521 && status <= 523:
		return CheckResult{Status: "fail",
			MessageKo:     fmt.Sprintf("Cloudflare가 서버에 연결하지 못했습니다 (오류 %d).", status),
			RemediationKo: "서버 방화벽에서 80, 443 포트가 열려 있는지, Cloudflare A 레코드가 이 서버 IP인지 확인해 주세요."}
	case probeErr != nil:
		return CheckResult{Status: "warn",
			MessageKo:     fmt.Sprintf("HTTPS 접속 실패 (%s): %v", via, probeErr),
			RemediationKo: "DNS를 바꾼 직후라면 인증서 발급까지 1~2분 기다려 주세요. 계속되면 80/443 포트와 DNS A 레코드를 확인해 주세요."}
	case status >= 500:
		return CheckResult{Status: "warn", MessageKo: fmt.Sprintf("HTTPS는 연결되지만 서버가 오류 %d를 돌려줍니다 (%s). 서비스가 실행 중인지 확인해 주세요.", status, via)}
	default:
		msg := fmt.Sprintf("HTTPS 정상 (%s, 응답 %d)", via, status)
		if behindCF {
			msg += " — WebSocket도 Cloudflare를 거쳐 그대로 연결됩니다"
		}
		return CheckResult{Status: "ok", MessageKo: msg}
	}
}

func reachableOnLAN(listenAddr string) bool {
	_, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return false
	}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && !n.IP.IsLoopback() && n.IP.To4() != nil {
			conn, err := net.DialTimeout("tcp", net.JoinHostPort(n.IP.String(), port), time.Second)
			if err == nil {
				_ = conn.Close()
				return true
			}
			return false
		}
	}
	return false
}
