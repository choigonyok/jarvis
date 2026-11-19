import { useEffect, useState } from "react";
import type { Me } from "../shared/api";
import { get } from "./lib/api";
import { CallProvider } from "./lib/call";
import { LiveProvider } from "./lib/live";
import { go, useLocation } from "./lib/router";
import { ToastProvider } from "./lib/toast";
import { ConfirmProvider } from "./ui/confirm";
import { AuthPage } from "./pages/Auth";
import { HomePage } from "./pages/Home";

export function App() {
  const [me, setMe] = useState<Me | null | undefined>(undefined);
  const loc = useLocation();

  useEffect(() => {
    get<{ me: Me | null }>("/me")
      .then((r) => setMe(r.me))
      .catch(() => setMe(null));
  }, []);

  // 로그인했으면 첫 화면은 우리 둥지
  useEffect(() => {
    if (me && !loc.path.startsWith("/h/")) go(`/h/${me.home}`, { replace: true });
  }, [me, loc.path]);

  if (me === undefined) return <div className="min-h-dvh" aria-busy="true" />;

  if (!me)
    return (
      <ToastProvider>
        <AuthPage onDone={(m) => setMe(m)} />
      </ToastProvider>
    );

  const slug = loc.path.startsWith("/h/") ? decodeURIComponent(loc.path.slice(3).split("/")[0]) : me.home;
  return (
    <ToastProvider>
      <ConfirmProvider>
        <LiveProvider enabled>
          <CallProvider partner={me.partner}>
            <HomePage key={slug} slug={slug} me={me} onMe={setMe} />
          </CallProvider>
        </LiveProvider>
      </ConfirmProvider>
    </ToastProvider>
  );
}
