import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Globe, ShieldCheck } from "lucide-react";

export interface IngressNodeData {
  domains: string[];
  networkName: string;
}

export function IngressNode({ data }: NodeProps) {
  const d = data as unknown as IngressNodeData;
  return (
    <div className="min-w-[240px] rounded-xl border border-emerald-500/30 bg-[#13181D] px-4 py-3 shadow-xl shadow-black/40">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-emerald-500/15 text-emerald-400">
            <Globe className="h-4 w-4" />
          </div>
          <div>
            <div className="text-xs font-semibold text-zinc-100">인터넷 진입점 (Caddy)</div>
            <div className="text-[11px] text-emerald-400/90 flex items-center gap-1">
              <ShieldCheck className="h-3 w-3" />
              <span>다중 도메인 · 자동 HTTPS</span>
            </div>
          </div>
        </div>
        <span className="h-2 w-2 rounded-full bg-emerald-400 shadow-[0_0_8px_#34d399]" />
      </div>

      <div className="mt-2.5 space-y-1 border-t border-zinc-800/80 pt-2">
        {d.domains.length === 0 ? (
          <div className="text-[11px] text-zinc-500">연결된 앱 도메인 대기 중</div>
        ) : (
          d.domains.slice(0, 4).map((host) => (
            <div key={host} className="truncate font-mono text-[11px] text-zinc-300">
              https://{host}
            </div>
          ))
        )}
        {d.domains.length > 4 && (
          <div className="text-[11px] text-zinc-500">외 {d.domains.length - 4}개 도메인</div>
        )}
      </div>

      <Handle
        type="source"
        position={Position.Right}
        className="!h-2.5 !w-2.5 !border-2 !border-[#13181D] !bg-emerald-400"
      />
    </div>
  );
}
