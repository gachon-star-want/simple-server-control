import { useEffect, useState } from "react";
import {
  AlertTriangle,
  Copy,
  Eye,
  EyeOff,
  FileText,
  Loader2,
  Lock,
  Plus,
  Rocket,
  Save,
  Trash2,
} from "lucide-react";
import { api } from "../lib/api";
import type { SecretInput, SecretView } from "../types";

interface SecretEditorProps {
  scope: "service" | "environment";
  targetId: string;
  secretsDirty?: boolean;
  onRedeploy?: () => void;
  onChanged?: () => void;
}

interface RowItem {
  key: string;
  value: string;
  is_sealed: boolean;
  is_system: boolean;
  revealed: boolean;
}

export function SecretEditor({
  scope,
  targetId,
  secretsDirty,
  onRedeploy,
  onChanged,
}: SecretEditorProps) {
  const [rows, setRows] = useState<RowItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [globalReveal, setGlobalReveal] = useState(false);
  const [showRawPaste, setShowRawPaste] = useState(false);
  const [rawText, setRawText] = useState("");
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  const load = async (reveal: boolean) => {
    setLoading(true);
    setError("");
    try {
      const res =
        scope === "service"
          ? await api.listServiceSecrets(targetId, reveal)
          : await api.listSharedSecrets(targetId, reveal);
      setRows(
        (res.secrets || []).map((s: SecretView) => ({
          key: s.key,
          value: s.value,
          is_sealed: s.is_sealed,
          is_system: s.is_system,
          revealed: !s.masked,
        }))
      );
    } catch (err: any) {
      setError(err.message || "시크릿 목록을 불러오지 못했습니다.");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void load(globalReveal);
  }, [targetId, scope, globalReveal]);

  const handleSaveAll = async () => {
    setSaving(true);
    setError("");
    setMessage("");
    try {
      const payload: SecretInput[] = rows
        .filter((r) => r.key.trim() !== "")
        .map((r) => ({
          key: r.key.trim(),
          value: r.value,
          is_sealed: r.is_sealed,
          is_system: r.is_system,
        }));

      // Warn user cleanly if any non-sealed item is still showing mask without reveal.
      if (!globalReveal && rows.some((r) => !r.is_sealed && r.value === "••••••••")) {
        setError("수정 저장 전에 오른쪽 '값 모두 보기(눈 아이콘)'를 눌러 기존 값을 로드한 뒤 저장해 주세요.");
        setSaving(false);
        return;
      }

      if (scope === "service") {
        await api.saveServiceSecrets(targetId, payload);
      } else {
        await api.saveSharedSecrets(targetId, payload);
      }
      setMessage("시크릿이 AES-256으로 암호화되어 저장되었습니다.");
      await load(globalReveal);
      onChanged?.();
    } catch (err: any) {
      setError(err.message || "시크릿 저장에 실패했습니다.");
    } finally {
      setSaving(false);
    }
  };

  const handleImportRaw = async () => {
    if (!rawText.trim()) return;
    setSaving(true);
    setError("");
    try {
      const res =
        scope === "service"
          ? await api.importServiceDotEnv(targetId, rawText, false)
          : await api.importSharedDotEnv(targetId, rawText, false);
      setRawText("");
      setShowRawPaste(false);
      setMessage(`${res.imported}개의 .env 변수를 가져왔습니다.`);
      await load(globalReveal);
      onChanged?.();
    } catch (err: any) {
      setError(err.message || ".env 파싱에 실패했습니다.");
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-4">
      {secretsDirty && onRedeploy && (
        <div className="flex items-center justify-between gap-3 rounded-xl border border-amber-500/35 bg-amber-500/10 px-4 py-3 text-xs text-amber-200">
          <div className="flex items-center gap-2">
            <AlertTriangle className="h-4 w-4 shrink-0 text-amber-400" />
            <span>시크릿이 수정되어 실행 중인 컨테이너에 아직 반영되지 않았습니다.</span>
          </div>
          <button
            onClick={onRedeploy}
            className="inline-flex shrink-0 items-center gap-1.5 rounded-lg bg-amber-500 px-3 py-1.5 font-semibold text-black hover:bg-amber-400"
          >
            <Rocket className="h-3.5 w-3.5" />
            <span>지금 재배포</span>
          </button>
        </div>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => setGlobalReveal((v) => !v)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-zinc-800 bg-[#0D1017] px-3 py-1.5 text-xs text-zinc-200 hover:border-zinc-700"
          >
            {globalReveal ? <EyeOff className="h-3.5 w-3.5" /> : <Eye className="h-3.5 w-3.5" />}
            <span>{globalReveal ? "값 숨기기" : "값 보기"}</span>
          </button>
          <button
            type="button"
            onClick={() => setShowRawPaste((v) => !v)}
            className="inline-flex items-center gap-1.5 rounded-lg border border-zinc-800 bg-[#0D1017] px-3 py-1.5 text-xs text-zinc-200 hover:border-zinc-700"
          >
            <FileText className="h-3.5 w-3.5" />
            <span>.env 통째로 붙여넣기</span>
          </button>
        </div>

        <button
          type="button"
          onClick={() => {
            if (!globalReveal && rows.length > 0) {
              setGlobalReveal(true);
            }
            setRows((prev) => [
              ...prev,
              { key: "", value: "", is_sealed: false, is_system: false, revealed: true },
            ]);
          }}
          className="inline-flex items-center gap-1 rounded-lg bg-zinc-800 px-3 py-1.5 text-xs font-medium text-zinc-100 hover:bg-zinc-700"
        >
          <Plus className="h-3.5 w-3.5" />
          <span>변수 추가</span>
        </button>
      </div>

      {showRawPaste && (
        <div className="space-y-2.5 rounded-xl border border-zinc-800 bg-[#0D1017] p-3.5">
          <div className="text-xs text-zinc-400">
            <code>.env</code> 내용을 그대로 붙여넣으세요 (주석 # 무시, 따옴표 자동 정리).
          </div>
          <textarea
            rows={5}
            value={rawText}
            onChange={(e) => setRawText(e.target.value)}
            placeholder={"OPENAI_API_KEY=sk-...\nJWT_SECRET=my-secret"}
            className="w-full rounded-lg border border-zinc-800 bg-[#080A0F] p-2.5 font-mono text-xs text-zinc-100 focus:border-violet-500 focus:outline-none"
          />
          <div className="flex justify-end gap-2">
            <button
              type="button"
              onClick={() => setShowRawPaste(false)}
              className="rounded-lg px-3 py-1.5 text-xs text-zinc-400 hover:text-zinc-200"
            >
              닫기
            </button>
            <button
              type="button"
              disabled={saving}
              onClick={handleImportRaw}
              className="rounded-lg bg-violet-600 px-3.5 py-1.5 text-xs font-medium text-white hover:bg-violet-500"
            >
              가져와서 암호화 저장
            </button>
          </div>
        </div>
      )}

      {loading ? (
        <div className="flex items-center gap-2 py-6 text-xs text-zinc-400">
          <Loader2 className="h-4 w-4 animate-spin" />
          <span>암호화된 시크릿 불러오는 중...</span>
        </div>
      ) : rows.length === 0 ? (
        <div className="rounded-xl border border-dashed border-zinc-800 py-8 text-center text-xs text-zinc-500">
          등록된 시크릿이 없습니다. &lsquo;변수 추가&rsquo; 또는 &lsquo;.env 통째로 붙여넣기&rsquo;를 눌러 추가하세요.
        </div>
      ) : (
        <div className="space-y-2">
          {rows.map((row, idx) => (
            <div key={idx} className="flex items-center gap-2">
              <input
                value={row.key}
                disabled={row.is_system}
                onChange={(e) =>
                  setRows((prev) =>
                    prev.map((item, i) => (i === idx ? { ...item, key: e.target.value } : item))
                  )
                }
                placeholder="KEY_NAME"
                className="w-2/5 rounded-lg border border-zinc-800 bg-[#0D1017] px-3 py-2 font-mono text-xs text-zinc-100 disabled:text-zinc-400 focus:border-violet-500 focus:outline-none"
              />
              <input
                type={row.revealed && !row.is_sealed ? "text" : "password"}
                value={row.value}
                disabled={!row.revealed && row.value === "••••••••"}
                onChange={(e) =>
                  setRows((prev) =>
                    prev.map((item, i) => (i === idx ? { ...item, value: e.target.value } : item))
                  )
                }
                placeholder={row.is_sealed ? "Sealed (작성 후 읽기 잠금)" : "값 또는 ${{postgres.DATABASE_URL}}"}
                className="flex-1 rounded-lg border border-zinc-800 bg-[#0D1017] px-3 py-2 font-mono text-xs text-zinc-100 focus:border-violet-500 focus:outline-none"
              />
              {row.revealed && !row.is_sealed && (
                <button
                  type="button"
                  title="복사"
                  onClick={() => navigator.clipboard?.writeText(row.value)}
                  className="rounded-lg border border-zinc-800 p-2 text-zinc-400 hover:text-zinc-200"
                >
                  <Copy className="h-3.5 w-3.5" />
                </button>
              )}
              <button
                type="button"
                title={row.is_sealed ? "Sealed Secret (저장 후 재열람 불가)" : "저장 후 읽기 잠금(Seal)"}
                onClick={() =>
                  setRows((prev) =>
                    prev.map((item, i) =>
                      i === idx ? { ...item, is_sealed: !item.is_sealed } : item
                    )
                  )
                }
                className={`rounded-lg border p-2 transition ${
                  row.is_sealed
                    ? "border-amber-500/40 bg-amber-500/15 text-amber-300"
                    : "border-zinc-800 text-zinc-500 hover:text-zinc-300"
                }`}
              >
                <Lock className="h-3.5 w-3.5" />
              </button>
              {!row.is_system && (
                <button
                  type="button"
                  onClick={() => setRows((prev) => prev.filter((_, i) => i !== idx))}
                  className="rounded-lg border border-zinc-800 p-2 text-zinc-500 hover:border-rose-500/40 hover:text-rose-400"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </button>
              )}
            </div>
          ))}
        </div>
      )}

      <div className="rounded-xl border border-zinc-800/70 bg-[#0D1017] px-3.5 py-2.5 text-[11px] text-zinc-400">
        팁: 같은 환경의 다른 서비스 값을 참조하려면 <code className="text-violet-300">${`{{postgres.DATABASE_URL}}`}</code> 형태로 입력할 수 있습니다.
      </div>

      {error && (
        <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3.5 py-2 text-xs text-rose-200">
          {error}
        </div>
      )}
      {message && (
        <div className="rounded-xl border border-emerald-500/40 bg-emerald-500/10 px-3.5 py-2 text-xs text-emerald-200">
          {message}
        </div>
      )}

      {rows.length > 0 && (
        <div className="flex justify-end">
          <button
            type="button"
            disabled={saving}
            onClick={handleSaveAll}
            className="inline-flex items-center gap-1.5 rounded-xl bg-violet-600 px-4 py-2 text-xs font-medium text-white hover:bg-violet-500 disabled:opacity-50"
          >
            {saving ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
            <span>암호화하여 저장</span>
          </button>
        </div>
      )}
    </div>
  );
}
