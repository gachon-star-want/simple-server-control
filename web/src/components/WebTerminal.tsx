import { useEffect, useRef, useState } from "react";
import { Terminal as TermIcon } from "lucide-react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { wsURL } from "../lib/api";

interface WebTerminalProps {
  serviceId: string;
  active: boolean;
}

export function WebTerminal({ serviceId, active }: WebTerminalProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [status, setStatus] = useState<"connecting" | "connected" | "closed">("connecting");

  useEffect(() => {
    if (!active || !containerRef.current) return;

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 12,
      fontFamily: "ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace",
      theme: {
        background: "#080A0F",
        foreground: "#E4E4E7",
        cursor: "#A78BFA",
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(containerRef.current);
    fit.fit();

    const ws = new WebSocket(wsURL(`/api/ws/services/${serviceId}/terminal`));
    ws.binaryType = "arraybuffer";

    ws.onopen = () => {
      setStatus("connected");
      ws.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
    };

    ws.onmessage = (evt) => {
      if (evt.data instanceof ArrayBuffer) {
        term.write(new Uint8Array(evt.data));
      } else {
        term.write(String(evt.data));
      }
    };

    ws.onclose = () => {
      setStatus("closed");
      term.write("\r\n\x1b[33m[터미널 세션이 종료되었습니다]\x1b[0m\r\n");
    };

    const disp = term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "input", data }));
      }
    });

    const handleResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: "resize", rows: term.rows, cols: term.cols }));
      }
    };
    window.addEventListener("resize", handleResize);

    return () => {
      window.removeEventListener("resize", handleResize);
      disp.dispose();
      ws.close();
      term.dispose();
    };
  }, [serviceId, active]);

  if (!active) {
    return (
      <div className="flex flex-col items-center justify-center rounded-xl border border-dashed border-zinc-800 py-12 text-xs text-zinc-500">
        <TermIcon className="mb-2 h-6 w-6 text-zinc-600" />
        <span>실행 중인 컨테이너가 없어 터미널을 열 수 없습니다.</span>
      </div>
    );
  }

  return (
    <div className="overflow-hidden rounded-xl border border-zinc-800 bg-[#080A0F]">
      <div className="flex items-center justify-between border-b border-zinc-800 px-3 py-2 text-[11px] text-zinc-400">
        <span className="flex items-center gap-1.5">
          <TermIcon className="h-3.5 w-3.5 text-violet-400" />
          컨테이너 셸 (/bin/sh)
        </span>
        <span className={status === "connected" ? "text-emerald-400" : "text-zinc-500"}>
          {status === "connected" ? "연결됨" : status === "connecting" ? "연결 중..." : "종료됨"}
        </span>
      </div>
      <div ref={containerRef} className="h-[320px] p-2" />
    </div>
  );
}
