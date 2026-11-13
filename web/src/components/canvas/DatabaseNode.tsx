import { Handle, Position, type NodeProps } from "@xyflow/react";
import { Database, HardDrive, Lock } from "lucide-react";
import type { CanvasService } from "../../types";
import { statusLabelKo } from "../../lib/api";

export interface DatabaseNodeData {
  service: CanvasService;
  selected?: boolean;
}

const dbLabels: Record<string, string> = {
  postgres: "PostgreSQL",
  redis: "Redis",
  mysql: "MySQL",
};

export function DatabaseNode({ data }: NodeProps) {
  const { service: svc, selected } = data as unknown as DatabaseNodeData;
  const running = svc.status === "running";

  return (
    <div
      className={`w-[255px] rounded-xl border bg-[#131722] p-3.5 shadow-xl shadow-black/50 transition-all ${
        selected ? "border-sky-400 ring-2 ring-sky-400/25" : "border-zinc-800 hover:border-sky-500/50"
      }`}
    >
      <Handle
        type="target"
        position={Position.Top}
        className="!h-2.5 !w-2.5 !border-2 !border-[#131722] !bg-sky-400"
      />

      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2.5 min-w-0">
          <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-sky-500/15 text-sky-300">
            <Database className="h-4 w-4" />
          </div>
          <div className="min-w-0">
            <div className="truncate text-sm font-semibold text-zinc-100">{svc.name}</div>
            <div className="text-[11px] text-sky-300/90">
              {dbLabels[svc.db_type] || svc.db_type || "Database"}
            </div>
          </div>
        </div>
        <span
          className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium ${
            running
              ? "border-emerald-500/30 bg-emerald-500/15 text-emerald-300"
              : "border-zinc-700 bg-zinc-800 text-zinc-400"
          }`}
        >
          <span className={`h-1.5 w-1.5 rounded-full ${running ? "bg-emerald-400" : "bg-zinc-500"}`} />
          {statusLabelKo(svc.status)}
        </span>
      </div>

      <div className="mt-2.5 flex items-center gap-1.5 rounded-lg border border-sky-500/20 bg-sky-500/10 px-2.5 py-1.5 text-[11px] text-sky-200">
        <Lock className="h-3 w-3 shrink-0 text-sky-400" />
        <span>내부 전용 네트워크 (외부 미노출)</span>
      </div>

      <div className="mt-2 flex items-center gap-2 text-[11px] text-zinc-400">
        <HardDrive className="h-3 w-3" />
        <span className="truncate font-mono">{svc.slug} · 볼륨 영구 저장</span>
      </div>
    </div>
  );
}
