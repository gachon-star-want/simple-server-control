import { useState } from "react";
import { ArrowRight, Link2, Loader2, X } from "lucide-react";
import { api } from "../lib/api";
import type { CanvasService } from "../types";

interface LinkSecretModalProps {
  projectId: string;
  environmentId: string;
  source: CanvasService;
  target: CanvasService;
  onClose: () => void;
  onLinked: () => void;
}

export function LinkSecretModal({
  projectId,
  environmentId,
  source,
  target,
  onClose,
  onLinked,
}: LinkSecretModalProps) {
  const defaultEnvKey = target.db_type === "redis" ? "REDIS_URL" : "DATABASE_URL";
  const [envKey, setEnvKey] = useState(defaultEnvKey);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  const handleConfirm = async (e: React.FormEvent) => {
    e.preventDefault();
    setSaving(true);
    setError("");
    try {
      await api.createLink(projectId, {
        environment_id: environmentId,
        source_service_id: source.id,
        target_service_id: target.id,
        injected_env_key: envKey.trim(),
      });
      onLinked();
      onClose();
    } catch (err: any) {
      setError(err.message || "연결 생성에 실패했습니다.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
      <div className="w-full max-w-md rounded-2xl border border-zinc-800 bg-[#13161E] shadow-2xl">
        <div className="flex items-center justify-between border-b border-zinc-800 px-5 py-4">
          <div className="flex items-center gap-2 text-sm font-semibold text-white">
            <Link2 className="h-4 w-4 text-sky-400" />
            <span>서비스 시크릿 연결</span>
          </div>
          <button onClick={onClose} className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800">
            <X className="h-4 w-4" />
          </button>
        </div>

        <form onSubmit={handleConfirm} className="p-5 space-y-4">
          <div className="flex items-center justify-center gap-3 rounded-xl border border-zinc-800 bg-[#0D1017] py-3 px-4 text-xs">
            <span className="font-semibold text-violet-300">{source.name}</span>
            <ArrowRight className="h-4 w-4 text-zinc-500" />
            <span className="font-semibold text-sky-300">{target.name}</span>
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-zinc-300">
              {source.name}에 주입할 환경 변수 이름
            </label>
            <input
              value={envKey}
              onChange={(e) => setEnvKey(e.target.value)}
              placeholder="DATABASE_URL"
              className="w-full rounded-xl border border-zinc-800 bg-[#0D1017] px-3.5 py-2 font-mono text-sm text-zinc-100 focus:border-sky-500 focus:outline-none"
            />
            <p className="mt-1.5 text-[11px] text-zinc-400">
              배포 시 자동으로 <code className="text-sky-300">${`{{${target.slug}.CONNECTION_URL}}`}</code> 값이 복호화되어 컨테이너 환경 변수로 주입됩니다.
            </p>
          </div>

          {error && (
            <div className="rounded-lg border border-rose-500/40 bg-rose-500/10 px-3 py-2 text-xs text-rose-200">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={onClose}
              className="rounded-xl border border-zinc-800 px-4 py-2 text-xs text-zinc-300 hover:bg-zinc-800"
            >
              취소
            </button>
            <button
              type="submit"
              disabled={saving}
              className="inline-flex items-center gap-1.5 rounded-xl bg-sky-600 px-4 py-2 text-xs font-medium text-white hover:bg-sky-500 disabled:opacity-50"
            >
              {saving && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
              <span>연결하기</span>
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
