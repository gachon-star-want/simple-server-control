import { Handle, Position, type NodeProps } from "@xyflow/react";
import { HardDrive } from "lucide-react";

export interface VolumeNodeData {
  dataPath: string;
  volumeName: string;
}

// Disk attached to an app's data folder; rendered as a child of the app node so it moves with it.
export function VolumeNode({ data }: NodeProps) {
  const { dataPath, volumeName } = data as unknown as VolumeNodeData;
  return (
    <div className="w-[200px] rounded-xl border border-amber-500/30 bg-[#17140D] px-3 py-2.5 shadow-lg shadow-black/40">
      <Handle type="target" position={Position.Top} className="!h-2 !w-2 !border-0 !bg-amber-400" />
      <div className="flex items-center gap-2">
        <HardDrive className="h-4 w-4 shrink-0 text-amber-300" />
        <div className="min-w-0">
          <div className="truncate font-mono text-xs font-semibold text-amber-100">{dataPath}</div>
          <div className="truncate text-[10px] text-amber-200/70" title={volumeName}>
            보관 디스크 · 배포해도 유지
          </div>
        </div>
      </div>
    </div>
  );
}
