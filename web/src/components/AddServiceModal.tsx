import { useEffect, useState } from "react";
import { Database, GitBranch, Globe, Layers, Loader2, X } from "lucide-react";
import { api } from "../lib/api";
import type { RepoSummary } from "../types";

interface AddServiceModalProps {
  projectId: string;
  environmentId: string;
  onClose: () => void;
  onCreated: () => void;
  onGoSettings: () => void;
}

export function AddServiceModal({
  projectId,
  environmentId,
  onClose,
  onCreated,
  onGoSettings,
}: AddServiceModalProps) {
  const [kind, setKind] = useState<"app" | "database">("app");
  const [name, setName] = useState("");
  const [repos, setRepos] = useState<RepoSummary[]>([]);
  const [branches, setBranches] = useState<string[]>([]);
  const [ghAppId, setGhAppId] = useState("");
  const [repoLoading, setRepoLoading] = useState(true);
  const [repoError, setRepoError] = useState("");

  const [repoFullName, setRepoFullName] = useState("");
  const [branch, setBranch] = useState("main");
  const [rootDir, setRootDir] = useState(".");
  const [buildPack, setBuildPack] = useState("auto");
  const [port, setPort] = useState("3000");
  const [healthPath, setHealthPath] = useState("/");
  const [dataPath, setDataPath] = useState("");
  const [autoDeploy, setAutoDeploy] = useState(true);
  const [autoDomain, setAutoDomain] = useState(true);
  const [deployNow, setDeployNow] = useState(true);
  const [dbType, setDbType] = useState("postgres");

  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    api
      .listGitHubRepos()
      .then((res) => {
        if (!active) return;
        setRepos(res.repositories || []);
        setGhAppId(res.app_id || "");
        if (res.repositories?.length > 0) {
          const first = res.repositories[0];
          setRepoFullName(first.full_name);
          setBranch(first.default_branch || "main");
          if (!name) {
            setName(first.full_name.split("/")[1] || "app");
          }
        }
      })
      .catch((err) => {
        if (!active) return;
        setRepoError(err.message || "");
      })
      .finally(() => {
        if (active) setRepoLoading(false);
      });
    return () => {
      active = false;
    };
  }, []);

  useEffect(() => {
    if (!repoFullName || !ghAppId) return;
    api
      .listGitHubBranches(repoFullName)
      .then((res) => setBranches(res.branches || []))
      .catch(() => setBranches([]));
  }, [repoFullName, ghAppId]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    if (!name.trim()) {
      setError("서비스 이름을 입력해 주세요.");
      return;
    }
    setSubmitting(true);
    try {
      await api.createService(projectId, {
        environment_id: environmentId,
        name: name.trim(),
        kind,
        db_type: kind === "database" ? dbType : undefined,
        github_app_id: kind === "app" ? ghAppId : undefined,
        repo_full_name: kind === "app" ? repoFullName.trim() : undefined,
        repo_branch: kind === "app" ? branch.trim() || "main" : undefined,
        root_dir: kind === "app" ? rootDir.trim() || "." : undefined,
        build_pack: kind === "app" ? buildPack : undefined,
        port: kind === "app" ? Number(port) || 3000 : undefined,
        healthcheck_path: kind === "app" ? healthPath.trim() || "/" : undefined,
        data_path: kind === "app" ? dataPath.trim() : undefined,
        auto_deploy: kind === "app" ? autoDeploy : false,
        auto_domain: kind === "app" ? autoDomain : false,
        deploy_now: kind === "app" ? deployNow && Boolean(repoFullName.trim()) : false,
      });
      onCreated();
      onClose();
    } catch (err: any) {
      setError(err.message || "서비스 생성에 실패했습니다.");
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="w-full max-w-xl rounded-2xl border border-zinc-800 bg-[#13161E] shadow-2xl">
        <div className="flex items-center justify-between border-b border-zinc-800 px-6 py-4">
          <h2 className="text-base font-semibold text-white">캔버스에 서비스 추가</h2>
          <button onClick={onClose} className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800 hover:text-white">
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={handleSubmit} className="max-h-[80vh] overflow-y-auto ssc-scroll p-6 space-y-5">
          <div className="grid grid-cols-2 gap-3">
            <button
              type="button"
              onClick={() => setKind("app")}
              className={`flex items-center gap-3 rounded-xl border p-3.5 text-left transition ${
                kind === "app"
                  ? "border-violet-500 bg-violet-500/10 text-white"
                  : "border-zinc-800 bg-[#0D1017] text-zinc-400 hover:border-zinc-700"
              }`}
            >
              <GitBranch className="h-5 w-5 text-violet-400 shrink-0" />
              <div>
                <div className="text-sm font-medium">GitHub 웹 앱</div>
                <div className="text-[11px] text-zinc-400">저장소 자동 빌드 · HTTPS 연결</div>
              </div>
            </button>

            <button
              type="button"
              onClick={() => {
                setKind("database");
                if (!name || name === repoFullName.split("/")[1]) {
                  setName(dbType);
                }
              }}
              className={`flex items-center gap-3 rounded-xl border p-3.5 text-left transition ${
                kind === "database"
                  ? "border-sky-500 bg-sky-500/10 text-white"
                  : "border-zinc-800 bg-[#0D1017] text-zinc-400 hover:border-zinc-700"
              }`}
            >
              <Database className="h-5 w-5 text-sky-400 shrink-0" />
              <div>
                <div className="text-sm font-medium">데이터베이스 (DB)</div>
                <div className="text-[11px] text-zinc-400">프로젝트 내부 격리 실행</div>
              </div>
            </button>
          </div>

          {kind === "app" ? (
            <>
              {repoLoading ? (
                <div className="flex items-center gap-2 rounded-xl border border-zinc-800 bg-[#0D1017] px-4 py-3 text-xs text-zinc-400">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  <span>연결된 GitHub 저장소 목록 불러오는 중...</span>
                </div>
              ) : repos.length > 0 ? (
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">GitHub 저장소 선택</label>
                  <select
                    value={repoFullName}
                    onChange={(e) => {
                      const val = e.target.value;
                      setRepoFullName(val);
                      const found = repos.find((r) => r.full_name === val);
                      if (found) {
                        setBranch(found.default_branch || "main");
                        setName(found.full_name.split("/")[1] || "");
                      }
                    }}
                    className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2.5 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
                  >
                    {repos.map((r) => (
                      <option key={r.id} value={r.full_name}>
                        {r.full_name} {r.private ? "(비공개)" : "(공개)"}
                      </option>
                    ))}
                  </select>
                </div>
              ) : (
                <div className="space-y-2 rounded-xl border border-amber-500/30 bg-amber-500/10 p-3.5 text-xs text-amber-200">
                  <div className="flex items-center justify-between">
                    <span>{repoError || "GitHub App이 아직 연결되지 않았습니다."}</span>
                    <button
                      type="button"
                      onClick={() => {
                        onClose();
                        onGoSettings();
                      }}
                      className="rounded-lg bg-amber-500/20 px-2.5 py-1 font-medium text-amber-100 hover:bg-amber-500/30"
                    >
                      1클릭 GitHub 연결하기 →
                    </button>
                  </div>
                  <input
                    value={repoFullName}
                    onChange={(e) => setRepoFullName(e.target.value)}
                    placeholder="또는 owner/repo 직접 입력 (예: gachon-star-want/pleumrouter)"
                    className="mt-1 w-full rounded-lg border border-zinc-700 bg-[#0D1017] px-3 py-2 text-xs text-zinc-100"
                  />
                </div>
              )}

              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">서비스 이름</label>
                  <input
                    value={name}
                    onChange={(e) => setName(e.target.value)}
                    placeholder="예: api-server"
                    className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">배포 브랜치</label>
                  {branches.length > 0 ? (
                    <select
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100"
                    >
                      {branches.map((b) => (
                        <option key={b} value={b}>
                          {b}
                        </option>
                      ))}
                    </select>
                  ) : (
                    <input
                      value={branch}
                      onChange={(e) => setBranch(e.target.value)}
                      placeholder="main"
                      className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100"
                    />
                  )}
                </div>
              </div>

              <div className="grid grid-cols-3 gap-3">
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">앱 내부 포트</label>
                  <input
                    type="number"
                    value={port}
                    onChange={(e) => setPort(e.target.value)}
                    className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-sm text-zinc-100"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">헬스체크 경로</label>
                  <input
                    value={healthPath}
                    onChange={(e) => setHealthPath(e.target.value)}
                    placeholder="/ 또는 /health"
                    className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-sm text-zinc-100"
                  />
                </div>
                <div>
                  <label className="mb-1.5 block text-xs font-medium text-zinc-300">빌드 방식</label>
                  <select
                    value={buildPack}
                    onChange={(e) => setBuildPack(e.target.value)}
                    className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3 py-2 text-sm text-zinc-100"
                  >
                    <option value="auto">자동 감지 (추천)</option>
                    <option value="railpack">Railpack</option>
                    <option value="dockerfile">Dockerfile</option>
                  </select>
                </div>
              </div>

              <div>
                <label className="mb-1.5 block text-xs font-medium text-zinc-300">보관 폴더 (선택)</label>
                <input
                  value={dataPath}
                  onChange={(e) => setDataPath(e.target.value)}
                  placeholder="예: /data — SQLite·업로드 파일처럼 배포 후에도 남아야 하는 폴더"
                  className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100"
                />
                <p className="mt-1 text-[11px] text-zinc-500">
                  지정하면 이 폴더가 디스크에 보관되어 다시 배포해도 남습니다. 대신 배포할 때 잠깐(수 초) 접속이 끊깁니다.
                </p>
              </div>

              <div>
                <label className="mb-1.5 block text-xs font-medium text-zinc-300">루트 디렉터리 (모노레포용)</label>
                <input
                  value={rootDir}
                  onChange={(e) => setRootDir(e.target.value)}
                  placeholder="."
                  className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100"
                />
              </div>

              <div className="space-y-2 rounded-xl border border-zinc-800/80 bg-[#0D1017] p-3.5 text-xs">
                <label className="flex items-center gap-2.5 cursor-pointer text-zinc-200">
                  <input
                    type="checkbox"
                    checked={autoDeploy}
                    onChange={(e) => setAutoDeploy(e.target.checked)}
                    className="rounded border-zinc-700 accent-violet-500"
                  />
                  <span>GitHub Push 시 자동 무중단 배포</span>
                </label>
                <label className="flex items-center gap-2.5 cursor-pointer text-zinc-200">
                  <input
                    type="checkbox"
                    checked={autoDomain}
                    onChange={(e) => setAutoDomain(e.target.checked)}
                    className="rounded border-zinc-700 accent-violet-500"
                  />
                  <Globe className="h-3.5 w-3.5 text-emerald-400" />
                  <span>접속 테스트용 무료 HTTPS 임시 도메인(*.sslip.io) 자동 연결</span>
                </label>
                <label className="flex items-center gap-2.5 cursor-pointer text-zinc-200">
                  <input
                    type="checkbox"
                    checked={deployNow}
                    onChange={(e) => setDeployNow(e.target.checked)}
                    className="rounded border-zinc-700 accent-violet-500"
                  />
                  <span>생성 직후 첫 배포 바로 시작하기</span>
                </label>
              </div>
            </>
          ) : (
            <div className="space-y-4">
              <div className="grid grid-cols-3 gap-2.5">
                {[
                  { id: "postgres", label: "PostgreSQL 16", desc: "가장 범용적인 RDB" },
                  { id: "redis", label: "Redis 7", desc: "캐시 · 세션 스토어" },
                  { id: "mysql", label: "MySQL 8.4", desc: "웹 서비스 호환 DB" },
                ].map((opt) => (
                  <button
                    key={opt.id}
                    type="button"
                    onClick={() => {
                      setDbType(opt.id);
                      setName(opt.id);
                    }}
                    className={`rounded-xl border p-3 text-left transition ${
                      dbType === opt.id
                        ? "border-sky-500 bg-sky-500/15 text-white"
                        : "border-zinc-800 bg-[#0D1017] text-zinc-400 hover:border-zinc-700"
                    }`}
                  >
                    <div className="text-sm font-semibold">{opt.label}</div>
                    <div className="text-[11px] text-zinc-400 mt-0.5">{opt.desc}</div>
                  </button>
                ))}
              </div>

              <div>
                <label className="mb-1.5 block text-xs font-medium text-zinc-300">데이터베이스 서비스 이름</label>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  placeholder="예: postgres"
                  className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 text-sm text-zinc-100 focus:border-sky-500 focus:outline-none"
                />
              </div>

              <div className="flex items-start gap-2 rounded-xl border border-sky-500/25 bg-sky-500/10 p-3 text-xs text-sky-200">
                <Layers className="h-4 w-4 shrink-0 text-sky-400 mt-0.5" />
                <span>
                  생성 시 비밀번호와 접속 URL(<code>DATABASE_URL</code>)이 AES-256으로 암호화되어 자동 생성되며, 이 프로젝트 내부 네트워크에만 안전하게 격리됩니다.
                </span>
              </div>
            </div>
          )}

          {error && (
            <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-2.5 text-xs text-rose-200">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2 pt-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl border border-zinc-800 px-4 py-2 text-sm text-zinc-300 hover:bg-zinc-800"
            >
              취소
            </button>
            <button
              type="submit"
              disabled={submitting}
              className="inline-flex items-center gap-2 rounded-xl bg-violet-600 px-5 py-2 text-sm font-medium text-white hover:bg-violet-500 disabled:opacity-50"
            >
              {submitting && <Loader2 className="h-4 w-4 animate-spin" />}
              <span>{kind === "database" ? "DB 생성 및 실행" : "서비스 추가"}</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
