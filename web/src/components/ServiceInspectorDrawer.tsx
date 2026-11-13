import { useEffect, useRef, useState } from "react";
import {
  Activity,
  AlertCircle,
  CheckCircle2,
  Cpu,
  ExternalLink,
  Globe,
  HardDrive,
  History,
  KeyRound,
  Loader2,
  Play,
  Plus,
  RefreshCw,
  Rocket,
  RotateCcw,
  Save,
  Settings2,
  Square,
  Terminal as TermIcon,
  Trash2,
  X,
} from "lucide-react";
import { api, formatBytes, statusLabelKo, wsURL } from "../lib/api";
import type { CanvasService, ContainerStats, Deployment, Domain } from "../types";
import { SecretEditor } from "./SecretEditor";
import { WebTerminal } from "./WebTerminal";

interface Props {
  service: CanvasService;
  onClose: () => void;
  onRefresh: () => void;
}

type TabId = "deployments" | "domains" | "secrets" | "logs" | "stats" | "settings";

const STAGES: Array<{ key: string; label: string }> = [
  { key: "cloning", label: "코드 준비" },
  { key: "building", label: "빌드" },
  { key: "starting_new", label: "새 컨테이너" },
  { key: "healthchecking", label: "헬스체크" },
  { key: "switching", label: "무중단 전환" },
  { key: "draining_old", label: "구버전 정리" },
];

export function ServiceInspectorDrawer({ service, onClose, onRefresh }: Props) {
  const [tab, setTab] = useState<TabId>(service.kind === "database" ? "secrets" : "deployments");
  const [deployments, setDeployments] = useState<Deployment[]>([]);
  const [activeDep, setActiveDep] = useState<Deployment | null>(service.latest_deployment || null);
  const [deployLogs, setDeployLogs] = useState<string[]>([]);
  const [liveStage, setLiveStage] = useState<string>(service.latest_deployment?.stage || "");

  const [runtimeLogs, setRuntimeLogs] = useState<string[]>([]);
  const [stats, setStats] = useState<ContainerStats | null>(null);

  const [domains, setDomains] = useState<Domain[]>(service.domains || []);
  const [newHostname, setNewHostname] = useState("");
  const [domainFeedback, setDomainFeedback] = useState("");

  const [branch, setBranch] = useState(service.repo_branch || "main");
  const [rootDir, setRootDir] = useState(service.root_dir || ".");
  const [port, setPort] = useState(String(service.port || 3000));
  const [hcPath, setHcPath] = useState(service.healthcheck_path || "/health");
  const [hcTimeout, setHcTimeout] = useState(String(service.healthcheck_timeout || 30));
  const [buildPack, setBuildPack] = useState(service.build_pack || "auto");
  const [autoDeploy, setAutoDeploy] = useState(service.auto_deploy);

  const [busy, setBusy] = useState(false);
  const [actionMsg, setActionMsg] = useState("");
  const [actionErr, setActionErr] = useState("");
  const logEndRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setDomains(service.domains || []);
    setBranch(service.repo_branch || "main");
    setRootDir(service.root_dir || ".");
    setPort(String(service.port || 3000));
    setHcPath(service.healthcheck_path || "/health");
    setHcTimeout(String(service.healthcheck_timeout || 30));
    setBuildPack(service.build_pack || "auto");
    setAutoDeploy(service.auto_deploy);
    void loadDeployments();
  }, [service.id]);

  const loadDeployments = async () => {
    try {
      const res = await api.listDeployments(service.id);
      const list = res.deployments || [];
      setDeployments(list);
      if (list.length > 0) {
        setActiveDep(list[0]);
        setLiveStage(list[0].stage);
      }
    } catch {
      /* ignore */
    }
  };

  // Subscribe to deployment WebSocket logs when an active deployment is selected.
  useEffect(() => {
    if (tab !== "deployments" || !activeDep?.id) return;
    setDeployLogs([]);
    setLiveStage(activeDep.stage);

    const ws = new WebSocket(wsURL(`/api/ws/deployments/${activeDep.id}/logs`));
    ws.onmessage = (e) => {
      try {
        const evt = JSON.parse(e.data);
        if (evt.stage) setLiveStage(evt.stage);
        if (evt.line) setDeployLogs((prev) => [...prev.slice(-400), evt.line]);
        if (evt.done) {
          void loadDeployments();
          onRefresh();
        }
      } catch {
        /* ignore */
      }
    };
    return () => ws.close();
  }, [tab, activeDep?.id]);

  // Stream live container runtime logs.
  useEffect(() => {
    if (tab !== "logs" || service.status !== "running") return;
    setRuntimeLogs([]);
    const ws = new WebSocket(wsURL(`/api/ws/services/${service.id}/logs`));
    ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.line) setRuntimeLogs((prev) => [...prev.slice(-500), msg.line]);
      } catch {
        /* ignore */
      }
    };
    return () => ws.close();
  }, [tab, service.id, service.status]);

  // Poll container stats on Stats tab.
  useEffect(() => {
    if (tab !== "stats" || service.status !== "running") return;
    let cancelled = false;
    const fetchOnce = async () => {
      try {
        const res = await api.getServiceStats(service.id);
        if (!cancelled) setStats(res.stats);
      } catch {
        /* ignore */
      }
    };
    void fetchOnce();
    const timer = window.setInterval(fetchOnce, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [tab, service.id, service.status]);

  useEffect(() => {
    logEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [deployLogs, runtimeLogs]);

  const handleDeploy = async () => {
    setBusy(true);
    setActionErr("");
    setActionMsg("");
    try {
      const res = await api.deployService(service.id);
      setActiveDep(res.deployment);
      setTab("deployments");
      await loadDeployments();
      onRefresh();
    } catch (err: any) {
      setActionErr(err.message || "배포를 시작하지 못했습니다.");
    } finally {
      setBusy(false);
    }
  };

  const handleStopOrRestart = async (mode: "stop" | "restart") => {
    setBusy(true);
    setActionErr("");
    setActionMsg("");
    try {
      if (mode === "stop") {
        await api.stopService(service.id);
        setActionMsg("컨테이너가 중지되었습니다.");
      } else {
        await api.restartService(service.id);
        setActionMsg("컨테이너가 재시작되었습니다.");
      }
      onRefresh();
    } catch (err: any) {
      setActionErr(err.message || "작업에 실패했습니다.");
    } finally {
      setBusy(false);
    }
  };

  const handleAddDomain = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!newHostname.trim()) return;
    setBusy(true);
    setDomainFeedback("");
    try {
      const res = await api.addDomain(service.id, newHostname.trim(), true);
      setNewHostname("");
      setDomainFeedback(res.dns_message || "도메인이 추가되었습니다.");
      const listRes = await api.listDomains(service.id);
      setDomains(listRes.domains || []);
      onRefresh();
    } catch (err: any) {
      setDomainFeedback(err.message || "도메인 추가 실패");
    } finally {
      setBusy(false);
    }
  };

  const handleVerifyDomain = async (domId: string) => {
    setBusy(true);
    setDomainFeedback("");
    try {
      const res = await api.verifyDomain(domId);
      setDomainFeedback(res.message);
      const listRes = await api.listDomains(service.id);
      setDomains(listRes.domains || []);
      onRefresh();
    } catch (err: any) {
      setDomainFeedback(err.message || "DNS 확인 실패");
    } finally {
      setBusy(false);
    }
  };

  const handleSaveSettings = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    setActionErr("");
    setActionMsg("");
    try {
      await api.updateService(service.id, {
        repo_branch: branch.trim(),
        root_dir: rootDir.trim() || ".",
        port: Number(port) || 3000,
        healthcheck_path: hcPath.trim() || "/",
        healthcheck_timeout: Number(hcTimeout) || 30,
        build_pack: buildPack,
        auto_deploy: autoDeploy,
      });
      setActionMsg("설정이 저장되었습니다. 변경 사항을 반영하려면 재배포하세요.");
      onRefresh();
    } catch (err: any) {
      setActionErr(err.message || "설정 저장 실패");
    } finally {
      setBusy(false);
    }
  };

  const handleDeleteService = async () => {
    if (!window.confirm(`'${service.name}' 서비스와 연결된 컨테이너를 삭제하시겠습니까?`)) return;
    setBusy(true);
    try {
      await api.deleteService(service.id);
      onClose();
      onRefresh();
    } catch (err: any) {
      setActionErr(err.message || "서비스 삭제 실패");
      setBusy(false);
    }
  };

  const stageIdx = STAGES.findIndex((s) => s.key === liveStage);

  const tabs: Array<{ id: TabId; label: string; icon: any; show: boolean }> = [
    { id: "deployments", label: "배포 & 진행", icon: Rocket, show: service.kind === "app" },
    { id: "domains", label: "도메인", icon: Globe, show: service.kind === "app" },
    { id: "secrets", label: "시크릿", icon: KeyRound, show: true },
    { id: "logs", label: "실시간 로그", icon: Activity, show: true },
    { id: "stats", label: "리소스 · 셸", icon: TermIcon, show: true },
    { id: "settings", label: "설정", icon: Settings2, show: true },
  ];

  return (
    <aside className="flex h-full w-full max-w-[600px] flex-col border-l border-zinc-800 bg-[#11141C] shadow-2xl">
      <div className="flex items-center justify-between border-b border-zinc-800 px-5 py-4">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h2 className="truncate text-base font-semibold text-white">{service.name}</h2>
            <span className="rounded-full border border-zinc-700 bg-zinc-800 px-2 py-0.5 text-[11px] text-zinc-200">
              {statusLabelKo(service.status)}
            </span>
          </div>
          <div className="mt-0.5 truncate text-xs text-zinc-400">
            {service.kind === "database"
              ? `${service.db_type.toUpperCase()} · 내부 호스트: ${service.slug}`
              : `${service.repo_full_name || "저장소 미연결"} @ ${service.repo_branch}`}
          </div>
        </div>

        <div className="flex items-center gap-1.5">
          {service.kind === "app" && (
            <button
              onClick={handleDeploy}
              disabled={busy}
              className="inline-flex items-center gap-1.5 rounded-xl bg-violet-600 px-3.5 py-2 text-xs font-semibold text-white hover:bg-violet-500 disabled:opacity-50"
            >
              {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Rocket className="h-3.5 w-3.5" />}
              <span>지금 배포</span>
            </button>
          )}
          {service.status === "running" ? (
            <button
              onClick={() => handleStopOrRestart("stop")}
              disabled={busy}
              title="컨테이너 중지"
              className="rounded-xl border border-zinc-800 p-2 text-zinc-300 hover:bg-zinc-800"
            >
              <Square className="h-3.5 w-3.5" />
            </button>
          ) : (
            <button
              onClick={() => handleStopOrRestart("restart")}
              disabled={busy}
              title="컨테이너 시작/재시작"
              className="rounded-xl border border-zinc-800 p-2 text-zinc-300 hover:bg-zinc-800"
            >
              <Play className="h-3.5 w-3.5" />
            </button>
          )}
          <button onClick={onClose} className="rounded-xl p-2 text-zinc-400 hover:bg-zinc-800 hover:text-white">
            <X className="h-4 w-4" />
          </button>
        </div>
      </div>

      <div className="flex gap-1 overflow-x-auto border-b border-zinc-800 px-4 py-2">
        {tabs
          .filter((t) => t.show)
          .map((t) => {
            const Icon = t.icon;
            return (
              <button
                key={t.id}
                onClick={() => setTab(t.id)}
                className={`inline-flex shrink-0 items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
                  tab === t.id
                    ? "bg-violet-600/20 text-violet-300 border border-violet-500/30"
                    : "text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-200"
                }`}
              >
                <Icon className="h-3.5 w-3.5" />
                <span>{t.label}</span>
              </button>
            );
          })}
      </div>

      <div className="flex-1 overflow-y-auto ssc-scroll p-5 space-y-4">
        {actionErr && (
          <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-2.5 text-xs text-rose-200">
            {actionErr}
          </div>
        )}
        {actionMsg && (
          <div className="rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-4 py-2.5 text-xs text-emerald-200">
            {actionMsg}
          </div>
        )}

        {tab === "deployments" && (
          <div className="space-y-4">
            {/* 6-Stage Zero-Downtime Stepper */}
            <div className="rounded-xl border border-zinc-800 bg-[#0D1017] p-4">
              <div className="mb-3 flex items-center justify-between text-xs">
                <span className="font-semibold text-zinc-200">무중단 배포 진행 단계</span>
                <span className="font-mono text-zinc-400">
                  {activeDep ? (activeDep.status === "succeeded" ? "완료됨" : activeDep.status === "failed" ? "실패" : liveStage || "대기") : "기록 없음"}
                </span>
              </div>
              <div className="grid grid-cols-6 gap-1.5">
                {STAGES.map((st, idx) => {
                  const done = activeDep?.status === "succeeded" || (stageIdx > idx && activeDep?.status !== "failed");
                  const current = liveStage === st.key && activeDep?.status === "running";
                  const failedHere = activeDep?.status === "failed" && liveStage === st.key;
                  return (
                    <div key={st.key} className="text-center">
                      <div
                        className={`h-1.5 rounded-full transition-all ${
                          failedHere
                            ? "bg-rose-500"
                            : done
                            ? "bg-emerald-400"
                            : current
                            ? "bg-amber-400 animate-pulse"
                            : "bg-zinc-800"
                        }`}
                      />
                      <div className="mt-1.5 text-[10px] text-zinc-400 truncate">{st.label}</div>
                    </div>
                  );
                })}
              </div>
            </div>

            {activeDep?.status === "failed" && activeDep.error_title_ko && (
              <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 p-4 space-y-1.5">
                <div className="flex items-center gap-2 text-xs font-semibold text-rose-200">
                  <AlertCircle className="h-4 w-4 text-rose-400 shrink-0" />
                  <span>{activeDep.error_title_ko}</span>
                </div>
                <p className="text-xs leading-relaxed text-rose-100/80">{activeDep.error_detail_ko}</p>
              </div>
            )}

            {/* Build / Deploy Live Log Console */}
            <div className="overflow-hidden rounded-xl border border-zinc-800 bg-[#080A0F]">
              <div className="flex items-center justify-between border-b border-zinc-800 px-3.5 py-2 text-[11px] text-zinc-400">
                <span>배포 로그 {activeDep?.commit_sha ? `(${activeDep.commit_sha})` : ""}</span>
                {activeDep?.status === "running" && (
                  <span className="inline-flex items-center gap-1 text-amber-300">
                    <Loader2 className="h-3 w-3 animate-spin" /> 실시간 수신 중
                  </span>
                )}
              </div>
              <pre className="max-h-64 overflow-y-auto ssc-scroll p-3.5 font-mono text-[11px] leading-relaxed text-zinc-300 whitespace-pre-wrap">
                {deployLogs.length > 0 ? deployLogs.join("\n") : "아직 배포 로그가 없습니다. '지금 배포'를 눌러 시작하세요."}
                <div ref={logEndRef} />
              </pre>
            </div>

            {/* Deployment History + Rollback */}
            <div className="space-y-2">
              <div className="flex items-center gap-1.5 text-xs font-semibold text-zinc-300">
                <History className="h-3.5 w-3.5" />
                <span>최근 배포 기록</span>
              </div>
              {deployments.map((d) => (
                <div
                  key={d.id}
                  onClick={() => setActiveDep(d)}
                  className={`flex cursor-pointer items-center justify-between rounded-xl border px-3.5 py-2.5 text-xs transition ${
                    activeDep?.id === d.id
                      ? "border-violet-500/50 bg-violet-500/10"
                      : "border-zinc-800 bg-[#0D1017] hover:border-zinc-700"
                  }`}
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      {d.status === "succeeded" ? (
                        <CheckCircle2 className="h-3.5 w-3.5 text-emerald-400 shrink-0" />
                      ) : d.status === "failed" ? (
                        <AlertCircle className="h-3.5 w-3.5 text-rose-400 shrink-0" />
                      ) : (
                        <Loader2 className="h-3.5 w-3.5 animate-spin text-amber-400 shrink-0" />
                      )}
                      <span className="truncate font-medium text-zinc-200">
                        {d.commit_message || d.trigger_type}
                      </span>
                    </div>
                    <div className="mt-0.5 text-[11px] text-zinc-500 font-mono">
                      {d.commit_sha || "manual"} · {new Date(d.started_at).toLocaleString("ko-KR")}
                    </div>
                  </div>
                  {d.status === "succeeded" && d.image_tag && d.image_tag !== service.active_image && (
                    <button
                      onClick={(e) => {
                        e.stopPropagation();
                        void api.rollbackService(service.id, d.id).then(onRefresh);
                      }}
                      className="inline-flex shrink-0 items-center gap-1 rounded-lg border border-zinc-700 px-2.5 py-1 text-[11px] text-zinc-200 hover:bg-zinc-800"
                    >
                      <RotateCcw className="h-3 w-3" />
                      <span>롤백</span>
                    </button>
                  )}
                </div>
              ))}
            </div>
          </div>
        )}

        {tab === "domains" && (
          <div className="space-y-4">
            <form onSubmit={handleAddDomain} className="flex gap-2">
              <input
                value={newHostname}
                onChange={(e) => setNewHostname(e.target.value)}
                placeholder="연결할 도메인 입력 (예: api.pleum.com)"
                className="flex-1 rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-xs text-zinc-100 focus:border-violet-500 focus:outline-none"
              />
              <button
                type="submit"
                disabled={busy}
                className="inline-flex items-center gap-1 rounded-xl bg-violet-600 px-3.5 py-2 text-xs font-medium text-white hover:bg-violet-500"
              >
                <Plus className="h-3.5 w-3.5" />
                <span>도메인 연결</span>
              </button>
            </form>

            {domainFeedback && (
              <div className="rounded-xl border border-sky-500/30 bg-sky-500/10 px-3.5 py-2.5 text-xs text-sky-200">
                {domainFeedback}
              </div>
            )}

            <div className="space-y-2">
              {domains.map((dom) => (
                <div
                  key={dom.id}
                  className="flex items-center justify-between gap-2 rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-3"
                >
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <a
                        href={`https://${dom.hostname}`}
                        target="_blank"
                        rel="noreferrer"
                        className="truncate font-mono text-xs text-zinc-100 hover:text-violet-300 flex items-center gap-1"
                      >
                        <span>{dom.hostname}</span>
                        <ExternalLink className="h-3 w-3 shrink-0" />
                      </a>
                      {dom.is_auto && (
                        <span className="rounded bg-zinc-800 px-1.5 py-0.5 text-[10px] text-zinc-400">자동</span>
                      )}
                    </div>
                    <div className="mt-1 text-[11px]">
                      {dom.dns_verified ? (
                        <span className="text-emerald-400">HTTPS 인증서 활성</span>
                      ) : (
                        <span className="text-amber-300">DNS A 레코드 연결 대기 중</span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5">
                    <button
                      onClick={() => handleVerifyDomain(dom.id)}
                      className="inline-flex items-center gap-1 rounded-lg border border-zinc-700 px-2.5 py-1 text-[11px] text-zinc-200 hover:bg-zinc-800"
                    >
                      <RefreshCw className="h-3 w-3" />
                      <span>DNS 확인</span>
                    </button>
                    <button
                      onClick={async () => {
                        await api.deleteDomain(dom.id);
                        const next = domains.filter((x) => x.id !== dom.id);
                        setDomains(next);
                        onRefresh();
                      }}
                      className="rounded-lg border border-zinc-800 p-1.5 text-zinc-500 hover:text-rose-400"
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        {tab === "secrets" && (
          <SecretEditor
            scope="service"
            targetId={service.id}
            secretsDirty={service.secrets_dirty}
            onRedeploy={service.kind === "app" ? handleDeploy : undefined}
            onChanged={onRefresh}
          />
        )}

        {tab === "logs" && (
          <div className="overflow-hidden rounded-xl border border-zinc-800 bg-[#080A0F]">
            <div className="flex items-center justify-between border-b border-zinc-800 px-3.5 py-2 text-[11px] text-zinc-400">
              <span>실시간 컨테이너 런타임 로그 (stdout / stderr)</span>
              <span className={service.status === "running" ? "text-emerald-400" : "text-zinc-500"}>
                {service.status === "running" ? "스트리밍 중" : "컨테이너 중지됨"}
              </span>
            </div>
            <pre className="max-h-[480px] overflow-y-auto ssc-scroll p-3.5 font-mono text-[11px] leading-relaxed text-zinc-300 whitespace-pre-wrap">
              {runtimeLogs.length > 0 ? runtimeLogs.join("\n") : "수신된 로그가 아직 없습니다."}
              <div ref={logEndRef} />
            </pre>
          </div>
        )}

        {tab === "stats" && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-xl border border-zinc-800 bg-[#0D1017] p-3.5">
                <div className="flex items-center gap-1.5 text-xs text-zinc-400">
                  <Cpu className="h-3.5 w-3.5 text-violet-400" />
                  <span>CPU 사용률</span>
                </div>
                <div className="mt-1.5 text-xl font-bold text-white">
                  {stats ? `${stats.cpu_percent.toFixed(1)}%` : "-"}
                </div>
              </div>
              <div className="rounded-xl border border-zinc-800 bg-[#0D1017] p-3.5">
                <div className="flex items-center gap-1.5 text-xs text-zinc-400">
                  <HardDrive className="h-3.5 w-3.5 text-sky-400" />
                  <span>메모리 사용량</span>
                </div>
                <div className="mt-1.5 text-xl font-bold text-white">
                  {stats ? formatBytes(stats.memory_bytes) : "-"}
                </div>
                {stats && stats.memory_limit > 0 && (
                  <div className="text-[11px] text-zinc-500">
                    상한 {formatBytes(stats.memory_limit)} ({stats.memory_percent.toFixed(1)}%)
                  </div>
                )}
              </div>
            </div>

            <WebTerminal serviceId={service.id} active={service.status === "running"} />
          </div>
        )}

        {tab === "settings" && (
          <form onSubmit={handleSaveSettings} className="space-y-4">
            {service.kind === "app" && (
              <>
                <div className="grid grid-cols-2 gap-3">
                  <div>
                    <label className="mb-1 block text-xs text-zinc-300">브랜치</label>
                    <input
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs text-zinc-300">루트 디렉터리</label>
                    <input
                      value={rootDir}
                      onChange={(e) => setRootDir(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                    />
                  </div>
                </div>
                <div className="grid grid-cols-3 gap-3">
                  <div>
                    <label className="mb-1 block text-xs text-zinc-300">내부 포트</label>
                    <input
                      type="number"
                      value={port}
                      onChange={(e) => setPort(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs text-zinc-300">헬스체크 경로</label>
                    <input
                      value={hcPath}
                      onChange={(e) => setHcPath(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                    />
                  </div>
                  <div>
                    <label className="mb-1 block text-xs text-zinc-300">빌드 방식</label>
                    <select
                      value={buildPack}
                      onChange={(e) => setBuildPack(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                    >
                      <option value="auto">자동 감지</option>
                      <option value="railpack">Railpack</option>
                      <option value="dockerfile">Dockerfile</option>
                    </select>
                  </div>
                </div>
                <label className="flex items-center gap-2 text-xs text-zinc-200 cursor-pointer">
                  <input
                    type="checkbox"
                    checked={autoDeploy}
                    onChange={(e) => setAutoDeploy(e.target.checked)}
                    className="accent-violet-500"
                  />
                  <span>GitHub Push 시 자동 배포</span>
                </label>
                <button
                  type="submit"
                  disabled={busy}
                  className="inline-flex items-center gap-1.5 rounded-xl bg-violet-600 px-4 py-2 text-xs font-medium text-white hover:bg-violet-500"
                >
                  <Save className="h-3.5 w-3.5" />
                  <span>설정 저장</span>
                </button>
              </>
            )}

            <div className="border-t border-zinc-800 pt-4">
              <button
                type="button"
                onClick={handleDeleteService}
                className="inline-flex items-center gap-1.5 rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-2 text-xs font-medium text-rose-300 hover:bg-rose-500/20"
              >
                <Trash2 className="h-3.5 w-3.5" />
                <span>이 서비스 영구 삭제</span>
              </button>
            </div>
          </form>
        )}
      </div>
    </aside>
  );
}
