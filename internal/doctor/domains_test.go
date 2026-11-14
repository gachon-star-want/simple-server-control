package doctor

import (
	"errors"
	"strings"
	"testing"
)

func TestClassifyHTTPSExplainsCloudflareFailures(t *testing.T) {
	if r := ClassifyHTTPS(526, nil, true); r.Status != "fail" || !strings.Contains(r.RemediationKo, "Full (strict)") {
		t.Fatalf("526 behind Cloudflare must point at SSL mode/Always Use HTTPS: %+v", r)
	}
	if r := ClassifyHTTPS(522, nil, true); r.Status != "fail" || !strings.Contains(r.RemediationKo, "443") {
		t.Fatalf("522 must point at ports: %+v", r)
	}
	if r := ClassifyHTTPS(0, errors.New("x509: certificate"), false); r.Status != "warn" {
		t.Fatalf("TLS error should warn: %+v", r)
	}
	if r := ClassifyHTTPS(200, nil, true); r.Status != "ok" {
		t.Fatalf("200 should be ok: %+v", r)
	}
	if r := CheckPanelExposure("127.0.0.1:8080"); r.Status != "ok" {
		t.Fatalf("loopback panel should be ok: %+v", r)
	}
	if r := CheckPanelExposure(":8080"); r.Status != "warn" {
		t.Fatalf("public panel should warn: %+v", r)
	}
}
