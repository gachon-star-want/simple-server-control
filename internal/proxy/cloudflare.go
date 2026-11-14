package proxy

import "net"

// shortcut: Cloudflare's published edge ranges (cloudflare.com/ips, checked 2026-11), hardcoded;
// refresh if Cloudflare adds ranges — a miss only means a proxied domain shows as "다른 IP".
var cloudflareRanges = func() []*net.IPNet {
	cidrs := []string{
		"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22",
		"141.101.64.0/18", "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20",
		"197.234.240.0/22", "198.41.128.0/17", "162.158.0.0/15", "104.16.0.0/13",
		"104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
		"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32",
		"2405:8100::/32", "2a06:98c0::/29", "2c0f:f248::/32",
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// IsCloudflareIP reports whether ip belongs to Cloudflare's proxy (orange cloud) edge.
func IsCloudflareIP(ip string) bool {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	for _, n := range cloudflareRanges {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// CloudflareGuideKo is the one setup rule that makes Caddy's Let's Encrypt certificate work behind
// the orange cloud: Caddy answers the HTTP-01 challenge on port 80, which Cloudflare's
// "Always Use HTTPS" would otherwise redirect away before it reaches the server.
const CloudflareGuideKo = "Cloudflare 설정: SSL/TLS 암호화 모드를 'Full (strict)'로, 'Always Use HTTPS'는 끄세요 " +
	"(HTTPS 전환은 서버의 Caddy가 대신 합니다). 꼭 켜야 한다면 Redirect Rule에서 /.well-known/acme-challenge/* 경로를 예외로 두세요. " +
	"'Flexible' 모드는 무한 리디렉션이 생기므로 쓰지 마세요."
