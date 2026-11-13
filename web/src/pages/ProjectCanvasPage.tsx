import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  applyNodeChanges,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
} from "@xyflow/react";
import { KeyRound, Loader2, Network, Plus, X } from "lucide-react";
import { api, wsURL } from "../lib/api";
import type { CanvasService, ProjectCanvas } from "../types";
import { IngressNode } from "../components/canvas/IngressNode";
import { AppNode } from "../components/canvas/AppNode";
import { DatabaseNode } from "../components/canvas/DatabaseNode";
import { AddServiceModal } from "../components/AddServiceModal";
import { LinkSecretModal } from "../components/LinkSecretModal";
import { SecretEditor } from "../components/SecretEditor";
import { ServiceInspectorDrawer } from "../components/ServiceInspectorDrawer";

const nodeTypes = {
  ingress: IngressNode,
  app: AppNode,
  database: DatabaseNode,
};

interface ProjectCanvasPageProps {
  projectId: string;
  onGoSettings: () => void;
}

export function ProjectCanvasPage({ projectId, onGoSettings }: ProjectCanvasPageProps) {
  const [canvas, setCanvas] = useState<ProjectCanvas | null>(null);
  const [envId, setEnvId] = useState<string | undefined>(undefined);
  const [loading, setLoading] = useState(true);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [selectedServiceId, setSelectedServiceId] = useState<string | null>(null);
  const [showAddModal, setShowAddModal] = useState(false);
  const [showSharedSecrets, setShowSharedSecrets] = useState(false);
  const [pendingLink, setPendingLink] = useState<{
    source: CanvasService;
    target: CanvasService;
  } | null>(null);
  const [error, setError] = useState("");

  const loadCanvas = useCallback(
    async (targetEnv?: string) => {
      try {
        const res = await api.getCanvas(projectId, targetEnv ?? envId);
        setCanvas(res);
        setEnvId(res.active_environment.id);
        setError("");
      } catch (err: any) {
        setError(err.message || "캔버스를 불러오지 못했습니다.");
      } finally {
        setLoading(false);
      }
    },
    [projectId, envId]
  );

  useEffect(() => {
    void loadCanvas(envId);
  }, [projectId, envId]);

  // Live Canvas WebSocket listener
  useEffect(() => {
    const ws = new WebSocket(wsURL(`/api/ws/projects/${projectId}/canvas`));
    ws.onmessage = () => {
      void loadCanvas(envId);
    };
    return () => ws.close();
  }, [projectId, envId, loadCanvas]);

  // Recompute React Flow nodes cleanly when canvas payload refreshes.
  useEffect(() => {
    if (!canvas) return;
    const allDomains = canvas.services.flatMap((s) => s.domains.map((d) => d.hostname));
    const nextNodes: Node[] = [
      {
        id: "ingress-root",
        type: "ingress",
        position: { x: 40, y: 170 },
        draggable: false,
        data: { domains: allDomains, networkName: canvas.project.network_name },
      },
      ...canvas.services.map((svc, index) => ({
        id: svc.id,
        type: svc.kind === "database" ? "database" : "app",
        position: {
          x: svc.pos_x || 370 + (index % 3) * 320,
          y: svc.pos_y || 100 + Math.floor(index / 3) * 230,
        },
        data: {
          service: svc,
          selected: svc.id === selectedServiceId,
        },
      })),
    ];
    setNodes(nextNodes);
  }, [canvas, selectedServiceId]);

  const edges = useMemo<Edge[]>(() => {
    if (!canvas) return [];
    const list: Edge[] = [];
    for (const svc of canvas.services) {
      if (svc.kind === "app" && svc.domains.length > 0) {
        list.push({
          id: `ingress-${svc.id}`,
          source: "ingress-root",
          target: svc.id,
          animated: svc.status === "running" || svc.status === "deploying",
          style: { stroke: svc.status === "running" ? "#34D399" : "#52525B", strokeWidth: 2 },
        });
      }
    }
    for (const lnk of canvas.links) {
      list.push({
        id: lnk.id,
        source: lnk.source_service_id,
        target: lnk.target_service_id,
        label: lnk.injected_env_key || "LINK",
        animated: true,
        style: { stroke: "#38BDF8", strokeWidth: 2, strokeDasharray: "5 4" },
        labelStyle: { fill: "#BAE6FD", fontSize: 10, fontFamily: "monospace" },
        labelBgStyle: { fill: "#0D131D", fillOpacity: 0.9 },
      });
    }
    return list;
  }, [canvas]);

  const onNodesChange = useCallback((changes: NodeChange[]) => {
    setNodes((nds) => applyNodeChanges(changes, nds));
  }, []);

  const handleNodeDragStop = useCallback(
    (_: unknown, dragged: Node) => {
      if (!canvas || dragged.id === "ingress-root") return;
      void api.savePositions(canvas.project.id, canvas.active_environment.id, [
        { service_id: dragged.id, pos_x: dragged.position.x, pos_y: dragged.position.y },
      ]);
    },
    [canvas]
  );

  const handleConnect = useCallback(
    (conn: Connection) => {
      if (!canvas || !conn.source || !conn.target) return;
      if (conn.source === "ingress-root" || conn.target === "ingress-root") return;
      const src = canvas.services.find((s) => s.id === conn.source);
      const dst = canvas.services.find((s) => s.id === conn.target);
      if (src && dst && src.id !== dst.id) {
        setPendingLink({ source: src, target: dst });
      }
    },
    [canvas]
  );

  const handleCreateEnvironment = async () => {
    if (!canvas) return;
    const nextName = window.prompt("새 환경 이름 입력 (예: staging, dev):");
    if (!nextName?.trim()) return;
    const res = await api.createEnvironment(canvas.project.id, nextName.trim());
    setEnvId(res.environment.id);
    await loadCanvas(res.environment.id);
  };

  const selectedService =
    canvas?.services.find((s) => s.id === selectedServiceId) || null;

  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-sm text-zinc-400">
        <Loader2 className="mr-2 h-5 w-5 animate-spin" />
        <span>프로젝트 캔버스를 불러오는 중...</span>
      </div>
    );
  }

  if (error || !canvas) {
    return (
      <div className="p-8 text-sm text-rose-300">
        {error || "캔버스를 찾을 수 없습니다."}
      </div>
    );
  }

  return (
    <div className="relative flex h-full w-full overflow-hidden bg-[#0B0D12]">
      {/* Canvas Top Toolbar */}
      <div className="pointer-events-none absolute top-4 left-4 right-4 z-10 flex flex-wrap items-center justify-between gap-2">
        <div className="pointer-events-auto flex items-center gap-2 rounded-xl border border-zinc-800 bg-[#13161E]/95 p-1.5 backdrop-blur shadow-xl">
          {canvas.environments.map((env) => (
            <button
              key={env.id}
              onClick={() => {
                setSelectedServiceId(null);
                setEnvId(env.id);
              }}
              className={`rounded-lg px-3 py-1 text-xs font-medium transition ${
                env.id === canvas.active_environment.id
                  ? "bg-violet-600 text-white"
                  : "text-zinc-400 hover:bg-zinc-800 hover:text-zinc-200"
              }`}
            >
              {env.name}
            </button>
          ))}
          <button
            onClick={handleCreateEnvironment}
            title="환경 추가 (예: staging)"
            className="rounded-lg px-2 py-1 text-xs text-zinc-400 hover:bg-zinc-800 hover:text-zinc-100"
          >
            + 환경
          </button>
          <span className="mx-1 h-4 w-px bg-zinc-800" />
          <span className="inline-flex items-center gap-1 px-2 text-[11px] font-mono text-zinc-400">
            <Network className="h-3.5 w-3.5 text-sky-400" />
            {canvas.project.network_name}
          </span>
        </div>

        <div className="pointer-events-auto flex items-center gap-2">
          <button
            onClick={() => setShowSharedSecrets(true)}
            className="inline-flex items-center gap-1.5 rounded-xl border border-zinc-800 bg-[#13161E]/95 px-3.5 py-2 text-xs font-medium text-zinc-200 hover:border-zinc-700 shadow-lg"
          >
            <KeyRound className="h-3.5 w-3.5 text-amber-400" />
            <span>공유 시크릿</span>
          </button>
          <button
            onClick={() => setShowAddModal(true)}
            className="inline-flex items-center gap-1.5 rounded-xl bg-violet-600 px-4 py-2 text-xs font-semibold text-white hover:bg-violet-500 shadow-lg shadow-violet-600/25"
          >
            <Plus className="h-4 w-4" />
            <span>서비스 / DB 추가</span>
          </button>
        </div>
      </div>

      {/* Main Interactive Railway-style React Flow Canvas */}
      <div className="flex-1 h-full">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          onNodesChange={onNodesChange}
          onNodeDragStop={handleNodeDragStop}
          onConnect={handleConnect}
          onNodeClick={(_, node) => {
            if (node.id !== "ingress-root") {
              setSelectedServiceId(node.id);
            }
          }}
          onEdgeClick={(_, edge) => {
            if (!edge.id.startsWith("ingress-") && window.confirm("이 서비스 간 시크릿 연결선을 해제할까요?")) {
              void api.deleteLink(canvas.project.id, edge.id).then(() => loadCanvas());
            }
          }}
          onPaneClick={() => setSelectedServiceId(null)}
          fitView
          fitViewOptions={{ padding: 0.25 }}
          colorMode="dark"
        >
          <Background variant={BackgroundVariant.Dots} gap={22} size={1.2} color="#222736" />
          <Controls />
          <MiniMap
            nodeColor={(n) =>
              n.type === "ingress" ? "#34D399" : n.type === "database" ? "#38BDF8" : "#8B5CF6"
            }
            maskColor="rgba(11, 13, 18, 0.75)"
          />
        </ReactFlow>
      </div>

      {selectedService && (
        <ServiceInspectorDrawer
          service={selectedService}
          onClose={() => setSelectedServiceId(null)}
          onRefresh={() => void loadCanvas()}
        />
      )}

      {showAddModal && (
        <AddServiceModal
          projectId={canvas.project.id}
          environmentId={canvas.active_environment.id}
          onClose={() => setShowAddModal(false)}
          onCreated={() => void loadCanvas()}
          onGoSettings={onGoSettings}
        />
      )}

      {pendingLink && (
        <LinkSecretModal
          projectId={canvas.project.id}
          environmentId={canvas.active_environment.id}
          source={pendingLink.source}
          target={pendingLink.target}
          onClose={() => setPendingLink(null)}
          onLinked={() => void loadCanvas()}
        />
      )}

      {showSharedSecrets && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4 backdrop-blur-sm">
          <div className="w-full max-w-2xl rounded-2xl border border-zinc-800 bg-[#13161E] shadow-2xl">
            <div className="flex items-center justify-between border-b border-zinc-800 px-6 py-4">
              <div>
                <h2 className="text-base font-semibold text-white">
                  공유 시크릿 ({canvas.project.name} · {canvas.active_environment.name})
                </h2>
                <p className="text-xs text-zinc-400">
                  이 환경의 모든 앱/서비스 컨테이너에 공통으로 주입되는 환경 변수입니다.
                </p>
              </div>
              <button
                onClick={() => setShowSharedSecrets(false)}
                className="rounded-lg p-1 text-zinc-400 hover:bg-zinc-800"
              >
                <X className="h-4 w-4" />
              </button>
            </div>
            <div className="max-h-[75vh] overflow-y-auto ssc-scroll p-6">
              <SecretEditor
                scope="environment"
                targetId={canvas.active_environment.id}
                onChanged={() => void loadCanvas()}
              />
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
