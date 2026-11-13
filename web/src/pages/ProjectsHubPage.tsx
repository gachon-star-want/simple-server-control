import { useEffect, useState } from "react";
import {
  ArrowRight,
  Box,
  FolderPlus,
  Globe,
  Loader2,
  Network,
  Plus,
  Trash2,
  X,
} from "lucide-react";
import { api } from "../lib/api";
import type { ProjectOverview } from "../types";

interface ProjectsHubPageProps {
  onOpenProject: (projectId: string) => void;
  onGoSettings: () => void;
}

export function ProjectsHubPage({ onOpenProject, onGoSettings }: ProjectsHubPageProps) {
  const [projects, setProjects] = useState<ProjectOverview[]>([]);
  const [loading, setLoading] = useState(true);
  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState("");
  const [desc, setDesc] = useState("");
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState("");

  const load = async () => {
    setLoading(true);
    try {
      const res = await api.listProjects();
      setProjects(res.projects || []);
    } catch (err: any) {
      setError(err.message || "프로젝트 목록을 불러오지 못했습니다.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load();
  }, []);

  const handleCreate = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setCreating(true);
    setError("");
    try {
      const res = await api.createProject({ name: name.trim(), description: desc.trim() });
      setShowCreate(false);
      setName("");
      setDesc("");
      onOpenProject(res.project.id);
    } catch (err: any) {
      setError(err.message || "프로젝트 생성에 실패했습니다.");
    } finally {
      setCreating(false);
    }
  };

  const handleDelete = async (e: React.MouseEvent, proj: ProjectOverview) => {
    e.stopPropagation();
    if (!window.confirm(`'${proj.name}' 프로젝트와 속한 모든 컨테이너/네트워크를 삭제하시겠습니까?`)) {
      return;
    }
    await api.deleteProject(proj.id);
    await load();
  };

  return (
    <div className="h-full overflow-y-auto ssc-scroll px-6 py-8">
      <div className="mx-auto max-w-6xl space-y-6">
        <div className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <h1 className="text-2xl font-bold text-white">내 프로젝트 허브</h1>
            <p className="mt-1 text-sm text-zinc-400">
              각 서비스별로 독립된 캔버스와 격리된 Docker 네트워크가 자동으로 분리 관리됩니다.
            </p>
          </div>
          <div className="flex items-center gap-2.5">
            <button
              onClick={onGoSettings}
              className="rounded-xl border border-zinc-800 bg-[#13161E] px-4 py-2.5 text-xs font-medium text-zinc-200 hover:border-zinc-700"
            >
              GitHub · 서버 진단
            </button>
            <button
              onClick={() => setShowCreate(true)}
              className="inline-flex items-center gap-2 rounded-xl bg-violet-600 px-4 py-2.5 text-sm font-semibold text-white hover:bg-violet-500 shadow-lg shadow-violet-600/20"
            >
              <Plus className="h-4 w-4" />
              <span>새 프로젝트</span>
            </button>
          </div>
        </div>

        {error && (
          <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-4 py-3 text-xs text-rose-200">
            {error}
          </div>
        )}

        {loading ? (
          <div className="flex items-center gap-2 py-16 text-sm text-zinc-400">
            <Loader2 className="h-5 w-5 animate-spin" />
            <span>프로젝트 목록을 불러오는 중...</span>
          </div>
        ) : projects.length === 0 ? (
          <div className="flex flex-col items-center justify-center rounded-2xl border border-dashed border-zinc-800 bg-[#11141C] py-20 px-6 text-center">
            <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-violet-500/15 text-violet-400 mb-4">
              <FolderPlus className="h-6 w-6" />
            </div>
            <h2 className="text-lg font-semibold text-white">첫 번째 프로젝트를 만들어보세요</h2>
            <p className="mt-1.5 max-w-md text-xs leading-relaxed text-zinc-400">
              예: &ldquo;PleumRouter&rdquo;, &ldquo;사내 백오피스&rdquo; 등 운영할 서비스마다 별도 프로젝트를 만들면 캔버스, 시크릿, 도메인, 컨테이너가 깔끔하게 분리됩니다.
            </p>
            <button
              onClick={() => setShowCreate(true)}
              className="mt-5 inline-flex items-center gap-2 rounded-xl bg-violet-600 px-5 py-2.5 text-sm font-semibold text-white hover:bg-violet-500"
            >
              <Plus className="h-4 w-4" />
              <span>새 프로젝트 만들기</span>
            </button>
          </div>
        ) : (
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
            {projects.map((p) => (
              <div
                key={p.id}
                onClick={() => onOpenProject(p.id)}
                className="group relative flex cursor-pointer flex-col justify-between rounded-2xl border border-zinc-800/90 bg-[#13161E] p-5 transition hover:border-violet-500/60 hover:bg-[#161A24] shadow-xl shadow-black/30"
              >
                <div>
                  <div className="flex items-start justify-between gap-2">
                    <div className="min-w-0">
                      <h3 className="truncate text-base font-semibold text-white group-hover:text-violet-300">
                        {p.name}
                      </h3>
                      <p className="mt-0.5 truncate text-xs text-zinc-400">
                        {p.description || "독립 실행 프로젝트"}
                      </p>
                    </div>
                    <button
                      onClick={(e) => handleDelete(e, p)}
                      title="프로젝트 삭제"
                      className="rounded-lg p-1.5 text-zinc-500 opacity-0 transition group-hover:opacity-100 hover:bg-zinc-800 hover:text-rose-400"
                    >
                      <Trash2 className="h-4 w-4" />
                    </button>
                  </div>

                  <div className="mt-4 flex flex-wrap gap-1.5 text-[11px]">
                    <span className="inline-flex items-center gap-1 rounded-md bg-emerald-500/15 px-2 py-0.5 text-emerald-300 border border-emerald-500/25">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-400" />
                      실행 중 {p.running_count}/{p.service_count}
                    </span>
                    {p.deploying_count > 0 && (
                      <span className="rounded-md bg-amber-500/15 px-2 py-0.5 text-amber-300">
                        배포 중 {p.deploying_count}
                      </span>
                    )}
                    {p.failed_count > 0 && (
                      <span className="rounded-md bg-rose-500/15 px-2 py-0.5 text-rose-300">
                        오류 {p.failed_count}
                      </span>
                    )}
                    <span className="inline-flex items-center gap-1 rounded-md bg-zinc-800 px-2 py-0.5 text-zinc-400 font-mono">
                      <Network className="h-3 w-3" />
                      {p.network_name}
                    </span>
                  </div>

                  {p.domains.length > 0 ? (
                    <div className="mt-3 space-y-1">
                      {p.domains.slice(0, 2).map((d) => (
                        <div key={d} className="flex items-center gap-1.5 truncate font-mono text-[11px] text-zinc-300">
                          <Globe className="h-3 w-3 shrink-0 text-emerald-400" />
                          <span className="truncate">{d}</span>
                        </div>
                      ))}
                    </div>
                  ) : (
                    <div className="mt-3 flex items-center gap-1.5 text-[11px] text-zinc-500">
                      <Box className="h-3 w-3" />
                      <span>캔버스에서 앱이나 DB를 추가하세요</span>
                    </div>
                  )}
                </div>

                <div className="mt-5 flex items-center justify-between border-t border-zinc-800/80 pt-3 text-xs text-zinc-400">
                  <span>환경 {p.environment_count}개</span>
                  <span className="inline-flex items-center gap-1 font-medium text-violet-400 group-hover:translate-x-0.5 transition-transform">
                    캔버스 열기 <ArrowRight className="h-3.5 w-3.5" />
                  </span>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {showCreate && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
          <div className="w-full max-w-md rounded-2xl border border-zinc-800 bg-[#13161E] shadow-2xl">
            <div className="flex items-center justify-between border-b border-zinc-800 px-6 py-4">
              <h2 className="text-base font-semibold text-white">새 프로젝트 만들기</h2>
              <button onClick={() => setShowCreate(false)} className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800">
                <X className="h-4 w-4" />
              </button>
            </div>
            <form onSubmit={handleCreate} className="p-6 space-y-4">
              <div>
                <label className="mb-1.5 block text-xs font-medium text-zinc-300">프로젝트 이름</label>
                <input
                  value={name}
                  onChange={(e) => setName(e.target.value)}
                  required
                  placeholder="예: pleumrouter"
                  className="w-full rounded-xl border border-zinc-800 bg-[#0B0D12] px-3.5 py-2.5 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
                />
              </div>
              <div>
                <label className="mb-1.5 block text-xs font-medium text-zinc-300">설명 (선택)</label>
                <input
                  value={desc}
                  onChange={(e) => setDesc(e.target.value)}
                  placeholder="예: LLM API 라우팅 및 백엔드"
                  className="w-full rounded-xl border border-zinc-800 bg-[#0B0D12] px-3.5 py-2.5 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
                />
              </div>
              <div className="flex justify-end gap-2 pt-2">
                <button
                  type="button"
                  onClick={() => setShowCreate(false)}
                  className="rounded-xl border border-zinc-800 px-4 py-2 text-sm text-zinc-300 hover:bg-zinc-800"
                >
                  취소
                </button>
                <button
                  type="submit"
                  disabled={creating}
                  className="inline-flex items-center gap-2 rounded-xl bg-violet-600 px-5 py-2 text-sm font-semibold text-white hover:bg-violet-500 disabled:opacity-50"
                >
                  {creating && <Loader2 className="h-4 w-4 animate-spin" />}
                  <span>프로젝트 생성</span>
                </button>
              </div>
            </form>
          </div>
        </div>
      )}
    </div>
  );
}
