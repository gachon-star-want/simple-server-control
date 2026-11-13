import { Handle, Position, type NodeProps } from "@xyflow/react";
import { AlertTriangle, Box, ExternalLink, GitBranch, KeyRound } from "lucide-react";
import type { CanvasService } from "../../types";
import { statusLabelKo } from "../../lib/api";

export interface AppNodeData {
  service: CanvasService;
  selected?: boolean;
}

const statusStyles: Record<string, { dot: string; badge: string; border: string }> = {
  running: {
    dot: "bg-emerald-400 shadow-[0_0_8px_#34d399]",
    badge: "bg-emerald-500/15 text-emerald-300 border-emerald-500/30",
    border: "border-zinc-800 hover:border-violet-500/60",
  },
  deploying: {
    dot: "bg-amber-400 animate-pulse",
    badge: "bg-amber-500/15 text-amber-300 border-amber-500/30",
    border: "border-amber-500/50",
  },
  failed: {
    dot: "bg-rose-500",
    badge: "bg-rose-500/15 text-rose-300 border-rose-500/30",
    border: "border-rose-500/50",
  },
  stopped: {
    dot: "bg-zinc-500",
    badge: "bg-zinc-700/40 text-zinc-300 border-zinc-700",
    border: "border-zinc-800",
  },
  idle: {
    dot: "bg-zinc-500",
    badge: "bg-zinc-800 text-zinc-400 border-zinc-700",
    border: "border-zinc-800 hover:border-zinc-700",
  },
};

export function AppNode({ data }: NodeProps) {
  const { service: svc, selected } = data as unknown as AppNodeData;
  const st = statusStyles[svc.status] ?? statusStyles.idle;
  const primaryDomain = svc.domains[0];

  return (
    <div
      className={`w-[270px] rounded-xl border bg-[#141720] p-3.5 shadow-xl shadow-black/50 transition-all ${
        selected ? "border-violet-500 ring-2 ring-violet-500/25" : st.border
      }`}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="!h-2.5 !w-2.5 !border-2 !border-[#141720] !bg-violet-400"
      />

      <div className="flex items-start justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-violet-500/15 text-violet-300">
            <Box className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold text-zinc-100">{svc.name}</div>
            <div className="truncate text-[11px] text-zinc-400 flex items-center gap-1">
              <GitBranch className="h-3 w-3 shrink-0" />
              <span className="truncate">
                {svc.repo_full_name ? `${svc.repo_full_name}@${svc.repo_branch}` : "저장소 미연결"}
              </span>
            </div>
          </div>
        </div>
        <span className={`inline-flex shrink-0 items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${st.badge}`}>
          <span className={`h-1.5 w-1.5 rounded-full ${st.dot}`} />
          {statusLabelKo(svc.status)}
        </span>
      </div>

      {primaryDomain && (
        <a
          href={`https://${primaryDomain.hostname}`}
          target="_blank"
          rel="noreferrer"
          onClick={(e) => e.stopPropagation()}
          className="mt-2.5 flex items-center justify-between gap-1 rounded-lg bg-[#0D1017] px-2.5 py-1.5 text-[11px] text-zinc-300 hover:text-violet-300 border border-zinc-800/80"
        >
          <span className="truncate font-mono">{primaryDomain.hostname}</span>
          <ExternalLink className="h-3 w-3 shrink-0 text-zinc-500" />
        </a>
      )}

      <div className="mt-2.5 flex flex-wrap items-center gap-1.5 text-[11px]">
        <span className="rounded-md bg-zinc-800/80 px-2 py-0.5 text-zinc-300">
          컨테이너 {svc.running_count}개
        </span>
        <span className="inline-flex items-center gap-1 rounded-md bg-zinc-800/80 px-2 py-0.5 text-zinc-300">
          <KeyRound className="h-3 w-3 text-zinc-400" />
          시크릿 {svc.secret_count}
        </span>
        <span className="rounded-md bg-zinc-800/80 px-2 py-0.5 text-zinc-400 font-mono">
          :{svc.port}
        </span>
      </div>

      {svc.secrets_dirty && (
        <div className="mt-2 flex items-center gap-1.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-2 py-1 text-[11px] text-amber-300">
          <AlertTriangle className="h-3.5 w-3.5 shrink-0" />
          <span>시크릿 변경됨 · 재배포 필요</span>
        </div>
      )}

      <Handle
        type="source"
        position={Position.Bottom}
        className="!h-2.5 !w-2.5 !border-2 !border-[#141720] !bg-sky-400"
      />
    </div>
  );
}
