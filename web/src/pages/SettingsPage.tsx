import { useEffect, useRef, useState } from "react";
import {
  AlertTriangle,
  CheckCircle2,
  GitBranch,
  Globe,
  Loader2,
  RefreshCw,
  ShieldCheck,
  Trash2,
  XCircle,
} from "lucide-react";
import { api } from "../lib/api";
import type { DoctorCheck, GitHubApp } from "../types";

export function SettingsPage() {
  const [apps, setApps] = useState<GitHubApp[]>([]);
  const [detectedOrigin, setDetectedOrigin] = useState("");
  const [publicOrigin, setPublicOrigin] = useState("");
  const [orgName, setOrgName] = useState("");
  const [manifestData, setManifestData] = useState<{ action_url: string; manifest: string } | null>(null);
  const [panelDomain, setPanelDomain] = useState("");
  const [checks, setChecks] = useState<DoctorCheck[]>([]);
  const [loading, setLoading] = useState(true);
  const [connecting, setConnecting] = useState(false);
  const [feedback, setFeedback] = useState("");
  const [error, setError] = useState("");

  const formRef = useRef<HTMLFormElement>(null);

  const loadAll = async () => {
    setLoading(true);
    try {
      const [ghRes, sysRes, docRes] = await Promise.all([
        api.githubStatus(),
        api.systemInfo(),
        api.systemDoctor(),
      ]);
      setApps(ghRes.apps || []);
      setDetectedOrigin(ghRes.detected_origin || window.location.origin);
      setPublicOrigin((prev) => prev || ghRes.detected_origin || window.location.origin);
      setPanelDomain(sysRes.panel_domain || "");
      setChecks(docRes.checks || []);
    } catch (err: any) {
      setError(err.message || "설정 정보를 불러오지 못했습니다.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.get("github") === "connected") {
      setFeedback("GitHub App 연결이 완료되었습니다! 이제 프로젝트 캔버스에서 저장소를 바로 배포할 수 있습니다.");
    } else if (params.get("github_error")) {
      setError("GitHub 연결 오류: " + params.get("github_error"));
    }
    void loadAll();
  }, []);

  const handleConnectGitHub = async () => {
    setConnecting(true);
    setError("");
    try {
      const res = await api.createGitHubManifest({
        app_name: `ssc-${window.location.hostname.replace(/[^a-zA-Z0-9-]/g, "-").slice(0, 18)}`,
        public_origin: publicOrigin.trim() || detectedOrigin,
        organization: orgName.trim(),
      });
      setManifestData({ action_url: res.action_url, manifest: res.manifest });
      window.setTimeout(() => {
        formRef.current?.submit();
      }, 60);
    } catch (err: any) {
      setError(err.message || "GitHub App Manifest 생성에 실패했습니다.");
      setConnecting(false);
    }
  };

  const handleSavePanelDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    setFeedback("");
    try {
      await api.setPanelDomain(panelDomain.trim());
      setFeedback("대시보드 도메인 설정이 반영되었습니다.");
    } catch (err: any) {
      setError(err.message || "도메인 설정 저장 실패");
    }
  };

  return (
    <div className="h-full overflow-y-auto ssc-scroll px-6 py-8">
      <div className="mx-auto max-w-4xl space-y-6">
        <div>
          <h1 className="text-2xl font-bold text-white">GitHub 연결 &amp; 서버 상태 진단</h1>
          <p className="mt-1 text-sm text-zinc-400">
            GitHub 저장소 자동 배포 연동과 Ubuntu VPS / Docker / Caddy 실행 환경을 한눈에 확인하세요.
          </p>
        </div>

        {feedback && (
          <div className="rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-4 py-3 text-xs text-emerald-200">
            {feedback}
          </div>
        )}
        {error && (
          <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-3 text-xs text-rose-200">
            {error}
          </div>
        )}

        {/* 1-Click GitHub App Card */}
        <section className="rounded-2xl border border-zinc-800 bg-[#13161E] p-6 space-y-4">
          <div className="flex items-start justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-zinc-800 text-white">
                <GitBranch className="h-5 w-5" />
              </div>
              <div>
                <h2 className="text-base font-semibold text-white">GitHub App 1클릭 자동 연동</h2>
                <p className="text-xs text-zinc-400">
                  개인/비공개 저장소 읽기 및 Push Webhook 자동 배포 권한을 버튼 한 번으로 설정합니다.
                </p>
              </div>
            </div>
          </div>

          {apps.length > 0 && (
            <div className="space-y-2">
              {apps.map((app) => (
                <div
                  key={app.id}
                  className="flex items-center justify-between rounded-xl border border-zinc-800 bg-[#0D1017] px-4 py-3 text-xs"
                >
                  <div>
                    <div className="flex items-center gap-2 font-semibold text-zinc-100">
                      <span>{app.name}</span>
                      {app.connected ? (
                        <span className="rounded-full bg-emerald-500/15 px-2 py-0.5 text-[11px] text-emerald-300">
                          연결됨 ({app.account_login})
                        </span>
                      ) : (
                        <span className="rounded-full bg-amber-500/15 px-2 py-0.5 text-[11px] text-amber-300">
                          설치 미완료
                        </span>
                      )}
                    </div>
                    <div className="mt-0.5 text-zinc-500 font-mono">
                      App ID {app.app_id} · Installation {app.installation_id || "-"}
                    </div>
                  </div>
                  <div className="flex items-center gap-2">
                    {!app.connected && app.slug && (
                      <a
                        href={`https://github.com/apps/${app.slug}/installations/new`}
                        className="rounded-lg bg-violet-600 px-3 py-1.5 font-medium text-white hover:bg-violet-500"
                      >
                        저장소 선택 완료하기
                      </a>
                    )}
                    <button
                      onClick={async () => {
                        await api.deleteGitHubApp(app.id);
                        await loadAll();
                      }}
                      className="rounded-lg border border-zinc-800 p-2 text-zinc-500 hover:text-rose-400"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          )}

          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <div>
              <label className="mb-1 block text-xs text-zinc-300">서버 접속 주소 (Webhook 콜백 URL)</label>
              <input
                value={publicOrigin}
                onChange={(e) => setPublicOrigin(e.target.value)}
                placeholder="https://panel.example.com"
                className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 font-mono text-xs text-zinc-100 focus:border-violet-500 focus:outline-none"
              />
            </div>
            <div>
              <label className="mb-1 block text-xs text-zinc-300">GitHub 조직명 (개인 계정은 비워두세요)</label>
              <input
                value={orgName}
                onChange={(e) => setOrgName(e.target.value)}
                placeholder="선택 입력 (예: my-org)"
                className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-xs text-zinc-100 focus:border-violet-500 focus:outline-none"
              />
            </div>
          </div>

          <button
            onClick={handleConnectGitHub}
            disabled={connecting}
            className="inline-flex items-center gap-2 rounded-xl bg-white px-4 py-2.5 text-xs font-semibold text-black hover:bg-zinc-200 disabled:opacity-50"
          >
            {connecting ? <Loader2 className="h-4 w-4 animate-spin" /> : <GitBranch className="h-4 w-4" />}
            <span>GitHub에서 1클릭 App 생성 &amp; 연결하기</span>
          </button>

          {manifestData && (
            <form ref={formRef} action={manifestData.action_url} method="post" className="hidden">
              <input type="hidden" name="manifest" value={manifestData.manifest} />
            </form>
          )}
        </section>

        {/* Panel Domain Card */}
        <section className="rounded-2xl border border-zinc-800 bg-[#13161E] p-6 space-y-3">
          <div className="flex items-center gap-2 text-base font-semibold text-white">
            <Globe className="h-4 w-4 text-emerald-400" />
            <span>컨트롤 패널 자체 도메인 (선택)</span>
          </div>
          <p className="text-xs text-zinc-400">
            서버 IP:8080 대신 <code className="text-zinc-200">https://panel.내도메인.com</code> 으로 안전하게 대시보드에 접속하고 GitHub Webhook HTTPS를 받으려면 연결할 도메인을 입력하세요.
          </p>
          <form onSubmit={handleSavePanelDomain} className="flex gap-2">
            <input
              value={panelDomain}
              onChange={(e) => setPanelDomain(e.target.value)}
              placeholder="예: panel.pleum.com"
              className="flex-1 rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-xs text-zinc-100"
            />
            <button
              type="submit"
              className="rounded-xl bg-zinc-800 px-4 py-2 text-xs font-medium text-white hover:bg-zinc-700"
            >
              저장 및 프록시 적용
            </button>
          </form>
        </section>

        {/* Ubuntu LTS / Docker / Caddy Doctor Card */}
        <section className="rounded-2xl border border-zinc-800 bg-[#13161E] p-6 space-y-4">
          <div className="flex items-center justify-between">
            <div className="flex items-center gap-2 text-base font-semibold text-white">
              <ShieldCheck className="h-4 w-4 text-violet-400" />
              <span>서버 환경 자가 진단 (ssc doctor)</span>
            </div>
            <button
              onClick={loadAll}
              disabled={loading}
              className="inline-flex items-center gap-1.5 rounded-xl border border-zinc-800 px-3 py-1.5 text-xs text-zinc-300 hover:bg-zinc-800"
            >
              <RefreshCw className={`h-3.5 w-3.5 ${loading ? "animate-spin" : ""}`} />
              <span>다시 검사</span>
            </button>
          </div>

          <div className="space-y-2.5">
            {checks.map((chk) => (
              <div
                key={chk.name}
                className="flex items-start gap-3 rounded-xl border border-zinc-800 bg-[#0D1017] px-4 py-3 text-xs"
              >
                {chk.status === "ok" ? (
                  <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-400" />
                ) : chk.status === "warn" ? (
                  <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-amber-400" />
                ) : (
                  <XCircle className="mt-0.5 h-4 w-4 shrink-0 text-rose-400" />
                )}
                <div className="space-y-1">
                  <div className="font-semibold text-zinc-100">{chk.name}</div>
                  <div className="text-zinc-300">{chk.message_ko}</div>
                  {chk.remediation_ko && (
                    <div className="text-[11px] text-amber-300/90">해결: {chk.remediation_ko}</div>
                  )}
                </div>
              </div>
            ))}
          </div>
        </section>
      </div>
    </div>
  );
}
