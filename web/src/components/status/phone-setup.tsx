"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";

type Setup = {
  link: string;
  url: string;
  signals: { url: string; token: string; examples: { when: string; body: string }[] };
};

/**
 * Connecting the phone, in the order it is done on the phone itself. The
 * setup holds the phone's credentials, so it is fetched only when asked
 * (behind the console login) and kept in this component's memory.
 *
 * Opening OwnTracks is two taps on purpose: iOS Safari ignores a jump to
 * another app's scheme that does not come straight from a tap, and a jump
 * made after waiting for the fetch no longer does. So the first tap fetches,
 * and the second is a plain link to owntracks:///config?inline=…, followed by
 * the browser itself. The link can also be copied, for pasting into Safari.
 */
export function PhoneSetup() {
  const [setup, setSetup] = useState<Setup | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [showSignals, setShowSignals] = useState(false);
  const [copied, setCopied] = useState<string | null>(null);

  async function load(): Promise<Setup | null> {
    if (setup) return setup;
    setBusy(true);
    try {
      const res = await fetch("/api/context/owntracks/setup", { cache: "no-store" });
      const body = await res.json();
      if (!res.ok) {
        setError(body.error ?? "설정을 불러오지 못했어요.");
        return null;
      }
      setSetup(body as Setup);
      setError(null);
      return body as Setup;
    } catch {
      setError("설정을 불러오지 못했어요.");
      return null;
    } finally {
      setBusy(false);
    }
  }


  async function copy(key: string, text: string) {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      setTimeout(() => setCopied((k) => (k === key ? null : k)), 1500);
    } catch {
      setError("복사하지 못했어요.");
    }
  }

  return (
    <section className="mt-9 sm:mt-11" aria-labelledby="group-phone">
      <h2 id="group-phone" className="mb-2.5 text-[12px] text-faint">
        폰 연결
      </h2>
      <div className="rounded-xl bg-glass px-4 py-3.5">
        <p className="text-[13px] leading-[1.6] text-dim text-pretty">
          위치(OwnTracks)와 기기 상태(단축어)를 jarvis로 보내도록 아이폰을 설정해요. 이 화면을 아이폰에서 열고 차례로 하세요.
        </p>
        <ol className="mt-3 space-y-3.5 text-[13.5px] leading-[1.6]">
          <Step n={1}>
            OwnTracks를 설치합니다.{" "}
            <a
              href="https://apps.apple.com/app/id692424691"
              className="text-dim underline decoration-edge underline-offset-[3px] hover:text-foreground"
            >
              App Store
            </a>
          </Step>
          <Step n={2}>
            OwnTracks 지도 왼쪽 위 ⓘ → Settings → Remote Control에서{" "}
            <span className="text-foreground">Allow external configuration</span>을 켭니다. 26.2.3부터 기본으로 꺼져 있어요.
          </Step>
          <Step n={3}>
            <span className="block">
              &ldquo;jarvis 설정 불러오기&rdquo; 다음에 &ldquo;OwnTracks 열어서 설정 넣기&rdquo;를 누르면 OwnTracks가 바뀌는 내용을 보여
              줘요. Continue를 누르면 서버 주소·계정·모니터링(Significant)·원격 설정이 한 번에 들어갑니다.
            </span>
            {setup ? (
              <span className="mt-2 flex flex-col gap-2 sm:flex-row">
                <a
                  href={setup.link}
                  className="inline-flex h-11 w-full items-center justify-center rounded-lg border border-border bg-background px-4 text-sm font-medium text-foreground outline-none hover:bg-muted focus-visible:ring-3 focus-visible:ring-ring/50 sm:w-auto"
                >
                  OwnTracks 열어서 설정 넣기
                </a>
                <Button variant="ghost" className="h-11 w-full sm:w-auto sm:px-3" onClick={() => void copy("link", setup.link)}>
                  {copied === "link" ? "복사했어요" : "링크 복사"}
                </Button>
              </span>
            ) : (
              <Button
                variant="outline"
                className="mt-2 h-11 w-full sm:w-auto sm:px-4"
                onClick={() => void load()}
                disabled={busy}
              >
                {busy ? "불러오는 중" : "jarvis 설정 불러오기"}
              </Button>
            )}
            <span className="mt-2 block text-[12px] text-faint">
              OwnTracks가 열렸는데 아무것도 바뀌지 않으면 2번 스위치가 꺼져 있는 거예요. 링크 복사 후 Safari 주소창에 붙여 넣어도 열려요.
            </span>
          </Step>
          <Step n={4}>
            위치 권한을 <span className="text-foreground">항상 허용</span>으로 둡니다. 집·회사 같은 영역은 OwnTracks에서
            만들면 jarvis에도 그대로 들어와요.
          </Step>
          <Step n={5}>
            <span className="block">
              (선택) 단축어 → 자동화에서 CarPlay·집중 모드·충전기를 계기로 고르고, 동작은 <span className="text-foreground">URL 내용 가져오기</span>{" "}
              하나만 둡니다: 방식 POST, 헤더 Authorization, 본문 JSON. &ldquo;즉시 실행&rdquo;을 켜고 &ldquo;실행 시 알림&rdquo;은 꺼요.
            </span>
            {!showSignals ? (
              <Button
                variant="ghost"
                className="mt-1 h-11 w-full sm:w-auto sm:px-3"
                onClick={() => void load().then((s) => s && setShowSignals(true))}
                disabled={busy}
              >
                단축어에 넣을 값 보기
              </Button>
            ) : setup ? (
              <ul className="mt-2 divide-y divide-edge-soft">
                <CopyRow label="URL" value={setup.signals.url} id="url" copied={copied} onCopy={copy} />
                <CopyRow
                  label="Authorization"
                  value={`Bearer ${setup.signals.token}`}
                  shown="Bearer ••••••"
                  id="token"
                  copied={copied}
                  onCopy={copy}
                />
                {setup.signals.examples.map((e) => (
                  <CopyRow key={e.when} label={e.when} value={e.body} id={e.when} copied={copied} onCopy={copy} />
                ))}
              </ul>
            ) : null}
          </Step>
        </ol>
        {error ? (
          <p role="alert" className="mt-3 text-[12.5px] text-reject">
            {error}
          </p>
        ) : null}
      </div>
    </section>
  );
}

function Step({ n, children }: { n: number; children: React.ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="tnum mt-px w-4 shrink-0 text-[12px] text-faint">{n}</span>
      <span className="min-w-0 flex-1 text-pretty text-dim">{children}</span>
    </li>
  );
}

function CopyRow({
  label,
  value,
  shown,
  id,
  copied,
  onCopy,
}: {
  label: string;
  value: string;
  shown?: string;
  id: string;
  copied: string | null;
  onCopy: (id: string, text: string) => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={() => onCopy(id, value)}
        className="flex min-h-12 w-full items-center gap-3 py-2 text-left outline-none focus-visible:ring-3 focus-visible:ring-ring/50"
      >
        <span className="min-w-0 flex-1">
          <span className="block text-[12px] text-faint">{label}</span>
          <span className="block truncate font-mono text-[12px] text-dim">{shown ?? value}</span>
        </span>
        {copied === id ? (
          <Check aria-label="복사됨" className="size-4 shrink-0 text-online" />
        ) : (
          <Copy aria-label="복사" className="size-4 shrink-0 text-faint" />
        )}
      </button>
    </li>
  );
}
