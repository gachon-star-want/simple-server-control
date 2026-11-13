import type { ReactNode } from "react";
import { ChevronRight, FolderKanban, LogOut, Server, Settings } from "lucide-react";
import { api } from "../lib/api";

interface AppLayoutProps {
  currentPath: string;
  onNavigate: (path: string) => void;
  onLoggedOut: () => void;
  breadcrumb?: string;
  rightSlot?: ReactNode;
  children: ReactNode;
}

export function AppLayout({
  currentPath,
  onNavigate,
  onLoggedOut,
  breadcrumb,
  rightSlot,
  children,
}: AppLayoutProps) {
  const handleLogout = async () => {
    try {
      await api.logout();
    } finally {
      onLoggedOut();
    }
  };

  return (
    <div className="flex h-full flex-col bg-[#0B0D12] text-zinc-100">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-zinc-800/80 bg-[#0F1219]/90 px-4 backdrop-blur">
        <div className="flex items-center gap-3 min-w-0">
          <button
            onClick={() => onNavigate("/")}
            className="flex items-center gap-2 rounded-lg px-2 py-1 text-sm font-semibold text-white hover:bg-zinc-800/60"
          >
            <div className="flex h-7 w-7 items-center justify-center rounded-lg bg-gradient-to-br from-violet-500 to-indigo-600 shadow-sm">
              <Server className="h-4 w-4 text-white" />
            </div>
            <span>Simple Server Control</span>
          </button>

          {breadcrumb && (
            <div className="flex items-center gap-2 text-sm text-zinc-400 min-w-0">
              <ChevronRight className="h-4 w-4 shrink-0 text-zinc-600" />
              <span className="truncate font-medium text-zinc-200">{breadcrumb}</span>
            </div>
          )}
        </div>

        <div className="flex items-center gap-2">
          {rightSlot}

          <button
            onClick={() => onNavigate("/")}
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
              currentPath === "/" || currentPath.startsWith("/projects")
                ? "bg-zinc-800 text-white"
                : "text-zinc-400 hover:bg-zinc-800/50 hover:text-zinc-200"
            }`}
          >
            <FolderKanban className="h-3.5 w-3.5" />
            <span>프로젝트</span>
          </button>

          <button
            onClick={() => onNavigate("/settings")}
            className={`inline-flex items-center gap-1.5 rounded-lg px-3 py-1.5 text-xs font-medium transition ${
              currentPath.startsWith("/settings")
                ? "bg-zinc-800 text-white"
                : "text-zinc-400 hover:bg-zinc-800/50 hover:text-zinc-200"
            }`}
          >
            <Settings className="h-3.5 w-3.5" />
            <span>GitHub · 서버 진단</span>
          </button>

          <button
            onClick={handleLogout}
            title="로그아웃"
            className="inline-flex items-center gap-1 rounded-lg px-2.5 py-1.5 text-xs text-zinc-400 hover:bg-zinc-800/60 hover:text-zinc-100"
          >
            <LogOut className="h-3.5 w-3.5" />
          </button>
        </div>
      </header>

      <main className="flex-1 overflow-hidden">{children}</main>
    </div>
  );
}
