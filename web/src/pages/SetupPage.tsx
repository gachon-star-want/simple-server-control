import { useState } from "react";
import { KeyRound, Loader2, Server, ShieldCheck } from "lucide-react";
import { api } from "../lib/api";

interface SetupPageProps {
  onSuccess: () => void;
}

export function SetupPage({ onSuccess }: SetupPageProps) {
  const [setupToken, setSetupToken] = useState("");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError("");
    setLoading(true);
    try {
      await api.setup({ setup_token: setupToken.trim(), email: email.trim(), password });
      onSuccess();
    } catch (err: any) {
      setError(err.message || "초기 설정에 실패했습니다.");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="flex min-h-full items-center justify-center bg-[#0B0D12] px-4 py-12">
      <div className="w-full max-w-md rounded-2xl border border-zinc-800 bg-[#13161E] p-7 shadow-2xl">
        <div className="flex items-center gap-3">
          <div className="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-violet-500 to-indigo-600">
            <Server className="h-5 w-5 text-white" />
          </div>
          <div>
            <h1 className="text-lg font-bold text-white">Simple Server Control 초기 설정</h1>
            <p className="text-xs text-zinc-400">서버 관리자 계정을 한 번만 등록하면 바로 시작할 수 있어요.</p>
          </div>
        </div>

        <div className="mt-5 rounded-xl border border-violet-500/25 bg-violet-500/10 p-3.5 text-xs text-violet-200 leading-relaxed">
          <div className="flex items-center gap-1.5 font-semibold text-violet-100 mb-1">
            <ShieldCheck className="h-4 w-4 text-violet-400" />
            <span>터미널 Setup Token 확인 방법</span>
          </div>
          서버 설치 완료 후 화면 또는 <code className="font-mono text-violet-100">/var/lib/ssc/setup.token</code> 에 표시된 1회용 토큰을 입력해 주세요.
        </div>

        <form onSubmit={handleSubmit} className="mt-5 space-y-4">
          <div>
            <label className="mb-1.5 flex items-center gap-1.5 text-xs font-medium text-zinc-300">
              <KeyRound className="h-3.5 w-3.5 text-zinc-400" />
              <span>Setup Token</span>
            </label>
            <input
              value={setupToken}
              onChange={(e) => setSetupToken(e.target.value)}
              required
              placeholder="터미널에 출력된 토큰 붙여넣기"
              className="w-full rounded-xl border border-zinc-800 bg-[#0B0D12] px-3.5 py-2.5 font-mono text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
            />
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-zinc-300">관리자 이메일</label>
            <input
              type="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
              placeholder="pm@company.com"
              className="w-full rounded-xl border border-zinc-800 bg-[#0B0D12] px-3.5 py-2.5 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
            />
          </div>

          <div>
            <label className="mb-1.5 block text-xs font-medium text-zinc-300">비밀번호 (8자 이상)</label>
            <input
              type="password"
              minLength={8}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              required
              placeholder="••••••••"
              className="w-full rounded-xl border border-zinc-800 bg-[#0B0D12] px-3.5 py-2.5 text-sm text-zinc-100 focus:border-violet-500 focus:outline-none"
            />
          </div>

          {error && (
            <div className="rounded-xl border border-rose-500/40 bg-rose-500/10 px-3.5 py-2.5 text-xs text-rose-200">
              {error}
            </div>
          )}

          <button
            type="submit"
            disabled={loading}
            className="flex w-full items-center justify-center gap-2 rounded-xl bg-violet-600 py-2.5 text-sm font-semibold text-white hover:bg-violet-500 disabled:opacity-50"
          >
            {loading && <Loader2 className="h-4 w-4 animate-spin" />}
            <span>관리자 설정 완료 &amp; 시작하기</span>
          </button>
        </form>
      </div>
    </div>
  );
}
