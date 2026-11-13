import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { api } from "./lib/api";
import type { AuthStatus } from "./types";
import { AppLayout } from "./components/AppLayout";
import { SetupPage } from "./pages/SetupPage";
import { LoginPage } from "./pages/LoginPage";
import { ProjectsHubPage } from "./pages/ProjectsHubPage";
import { ProjectCanvasPage } from "./pages/ProjectCanvasPage";
import { SettingsPage } from "./pages/SettingsPage";

export function App() {
  const [auth, setAuth] = useState<AuthStatus | null>(null);
  const [path, setPath] = useState<string>(() => window.location.pathname || "/");

  const refreshAuth = async () => {
    try {
      const st = await api.authStatus();
      setAuth(st);
    } catch {
      setAuth({ needs_setup: false, authenticated: false });
    }
  };

  useEffect(() => {
    void refreshAuth();
    const onPop = () => setPath(window.location.pathname || "/");
    window.addEventListener("popstate", onPop);
    return () => window.removeEventListener("popstate", onPop);
  }, []);

  const navigate = (next: string) => {
    if (next !== path) {
      window.history.pushState({}, "", next);
      setPath(next);
    }
  };

  if (!auth) {
    return (
      <div className="flex h-full items-center justify-center bg-[#0B0D12] text-sm text-zinc-400">
        <Loader2 className="mr-2 h-5 w-5 animate-spin text-violet-400" />
        <span>Simple Server Control 불러오는 중...</span>
      </div>
    );
  }

  if (auth.needs_setup) {
    return <SetupPage onSuccess={() => void refreshAuth()} />;
  }

  if (!auth.authenticated) {
    return <LoginPage onSuccess={() => void refreshAuth()} />;
  }

  const projectMatch = path.match(/^\/projects\/([^/]+)/);
  const activeProjectId = projectMatch ? projectMatch[1] : null;

  return (
    <AppLayout
      currentPath={path}
      onNavigate={navigate}
      onLoggedOut={() => {
        setAuth({ needs_setup: false, authenticated: false });
        navigate("/");
      }}
      breadcrumb={activeProjectId ? "프로젝트 캔버스" : path.startsWith("/settings") ? "서버 & GitHub 설정" : undefined}
    >
      {path.startsWith("/settings") ? (
        <SettingsPage />
      ) : activeProjectId ? (
        <ProjectCanvasPage
          projectId={activeProjectId}
          onGoSettings={() => navigate("/settings")}
        />
      ) : (
        <ProjectsHubPage
          onOpenProject={(id) => navigate(`/projects/${id}`)}
          onGoSettings={() => navigate("/settings")}
        />
      )}
    </AppLayout>
  );
}
export default App;
