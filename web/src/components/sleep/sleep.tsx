"use client";

import type { ReactNode } from "react";
import Link from "next/link";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { Header, PageTop } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { TabBar } from "@/components/shell/tab-bar";
import { NightBand, StageLegend } from "@/components/sleep/band";
import { Recent, type Days } from "@/components/sleep/diary";
import { Button } from "@/components/ui/button";
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { useKept } from "@/lib/keep";
import { type Night, againstUsual, clockAt, daysBetween, duration, durationParts, readSleep } from "@/lib/sleep";
import { failureText, SAY } from "@/lib/say";
import { isPending } from "@/lib/thread";
import { useResource } from "@/lib/use-resource";
import { useThread } from "@/lib/use-thread";
import { dateLabel, dateTime } from "@/lib/when";

const COULD_NOT = "수면 기록을 불러오지 못했어요.";

/**
 * The operator's nights, as the phone sends them each morning (context-svc
 * /sleep): the screen opens on the night just slept - how long, in figures as
 * big as the console's other first answers, and the night itself as a band
 * from bed to waking with its depth hanging in ink - and under it the last
 * week or month as a sleep diary on one clock, which says at once how long,
 * when, and how regular. Pressing a night in the diary, or stepping with the
 * arrows, reads that night at the top.
 */
export function Sleep() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const read = useResource("sleep", (signal) => readSleep(signal));
  const data = read.data;
  const error = read.error ? failureText(read.error, COULD_NOT) : null;
  // The diary's length and the night read stay across a tab switch (lib/keep.ts).
  const [days, setDays] = useKept<Days>("sleep:days", "7");
  const [picked, setPicked] = useKept<string | null>("sleep:night", null);

  const nights = data?.nights ?? [];
  const index = Math.max(0, nights.findIndex((n) => n.night === picked));
  const shown = nights[index];
  const summary = data ? (days === "7" ? data.summary.week : data.summary.month) : null;

  return (
    <div className="flex h-full min-h-0 flex-col">
      <PageTop>
        <Header connection={connection} pending={waiting.length} />
        <StandingBar pending={waiting} />
      </PageTop>

      <main className="scrollbar-hairline inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain under-top pb-tabbar">
        <div className="mx-auto w-full max-w-page px-4 pt-6 pb-12 sm:px-8 sm:pt-10">
          {error && !data ? (
            <Failed onRetry={read.reload} className="max-w-[34rem] py-2 break-keep text-pretty">
              {COULD_NOT} {error === COULD_NOT ? "" : `${error} `}계속 이러면 상태 탭에서 context 서비스가 응답하는지 보세요.
            </Failed>
          ) : null}
          {error && data ? <AsOf at={read.readAt} failed onRetry={() => void read.reload()} className="mb-3" /> : null}
          {!data && !error ? <Placeholder /> : null}
          {data && nights.length === 0 ? <NoNights /> : null}

          {data && shown && summary ? (
            <>
              <LastNight
                night={shown}
                nights={nights}
                today={data.to}
                older={nights[index + 1]}
                newer={index > 0 ? nights[index - 1] : undefined}
                onShow={setPicked}
              />
              <div className="mt-11 sm:mt-14">
                <Recent
                  nights={nights}
                  last={data.to}
                  days={days}
                  onDays={setDays}
                  summary={summary}
                  shown={shown.night}
                  onShow={setPicked}
                />
              </div>
              <p className="mt-8 max-w-[34rem] text-aside leading-relaxed break-keep text-pretty text-faint">
                밤은 깬 날의 것이에요. 단계가 언제였는지는 {data.keepDays}일 동안만 남고, 그 뒤에는 합계만 남아요. 낮잠은 평균에 넣지 않아요.
              </p>
            </>
          ) : null}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}

/**
 * One night at the top of the screen: which night, how long asleep - the
 * figure - and against the operator's own usual, the band, the stages, the
 * day's naps, and who sent it when.
 */
function LastNight({
  night,
  nights,
  today,
  older,
  newer,
  onShow,
}: {
  night: Night;
  nights: Night[];
  /** Today on the server's clock (Seoul). */
  today: string;
  older?: Night;
  newer?: Night;
  onShow: (night: string) => void;
}) {
  const latest = !newer;
  const name = latest && night.night === today ? "지난밤" : dateLabel(night.night);
  const usual = night.bedOnly ? null : againstUsual(nights, night);
  const age = daysBetween(night.night, today);
  return (
    <section aria-labelledby="sleep-night">
      <div className="flex items-center justify-between gap-3">
        <h2 id="sleep-night" className="text-ui text-dim">
          {name}
        </h2>
        <div className="-me-2.5 flex shrink-0 items-center">
          <Button
            variant="quiet"
            size="icon-touch"
            aria-label="이전 밤"
            disabled={!older}
            onClick={() => older && onShow(older.night)}
          >
            <ChevronLeft aria-hidden />
          </Button>
          <Button
            variant="quiet"
            size="icon-touch"
            aria-label="다음 밤"
            disabled={!newer}
            onClick={() => newer && onShow(newer.night)}
          >
            <ChevronRight aria-hidden />
          </Button>
        </div>
      </div>

      <p className="tnum mt-1 flex flex-wrap items-baseline gap-x-2 text-display-lg leading-none font-semibold tracking-tight text-foreground">
        {durationParts(night.asleepMinutes).map((p) => (
          <span key={p.unit}>
            {p.n}
            <span className="ms-0.5 text-title font-medium tracking-normal text-dim">{p.unit}</span>
          </span>
        ))}
      </p>
      <p className="mt-2.5 text-ui text-dim">{night.bedOnly ? "침대에 있던 시간이에요" : (usual ?? "잔 시간")}</p>

      <div className="mt-7">
        <NightBand night={night} />
      </div>
      <StageLegend night={night} className="mt-3.5" />

      {night.naps.length > 0 ? (
        <p className="mt-3 text-aside text-dim">
          낮잠{" "}
          {night.naps.map((n, i) => (
            <span key={n.bedAt} className="tnum">
              {i > 0 ? ", " : ""}
              {clockAt(n.bedAt)}–{clockAt(n.wakeAt)} ({duration(n.asleepMinutes)})
            </span>
          ))}
        </p>
      ) : null}
      <p className="mt-3 truncate text-meta text-faint">
        {[night.source, night.updatedAt ? `${dateTime(night.updatedAt)} 받음` : ""].filter(Boolean).join(" · ")}
      </p>
      {latest && age >= 2 ? (
        <p className="mt-2 text-aside text-dim">
          마지막 기록이 {age}일 전이에요. 아침마다 보내는 자동화가 도는지 보세요.
        </p>
      ) : null}
    </section>
  );
}

/** Nothing has come yet: what will be here, and how to send it - where the address and token are, never them. */
function NoNights() {
  return (
    <Empty align="start" className="max-w-[34rem] py-4">
      <EmptyHeader className="gap-2">
        <EmptyTitle asChild className="text-foreground">
          <h2>아직 수면 기록이 없어요</h2>
        </EmptyTitle>
        <EmptyDescription>
          아이폰이 아침마다 지난밤 수면을 보내면 여기에 하루 한 줄씩 쌓여요. 단축어 자동화 하나면 되고, Health Auto
          Export를 쓰고 있다면 그 앱의 REST API 자동화로도 돼요.
        </EmptyDescription>
      </EmptyHeader>
      <EmptyContent className="max-w-none gap-4">
        <ol className="space-y-2.5 text-ui leading-[1.6] text-dim">
          <Step n={1}>
            단축어 → 자동화 → 새로운 자동화 → <span className="text-foreground">특정 시간</span>(매일 오전 10시), 즉시 실행.
          </Step>
          <Step n={2}>
            <span className="text-foreground">건강 샘플 찾기</span>(수면 분석, 시작 날짜가 지난 1일 이내) 다음{" "}
            <span className="text-foreground">각 항목을 반복</span>하며 사전 하나: start·end 는 시작·종료 날짜(ISO 8601), value 는 값.
          </Step>
          <Step n={3}>
            <span className="text-foreground">URL 내용 가져오기</span>: POST, 헤더 Authorization, 본문 JSON 의 samples 에 반복 결과.
          </Step>
        </ol>
        <Button asChild variant="raised" size="touch" className="w-full sm:w-auto">
          <Link href="/status?focus=check:phone">폰 연결 열기</Link>
        </Button>
        <p className="text-aside leading-relaxed text-faint">
          보낼 주소와 토큰은 상태 → 폰 연결의 ‘단축어에 넣을 값 보기’에 있어요. 토큰은 CarPlay·집중 모드 단축어와 같은 신호
          토큰이고, 화면에는 보이지 않고 복사만 돼요.
        </p>
      </EmptyContent>
    </Empty>
  );
}

function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex gap-3">
      <span className="tnum mt-px w-4 shrink-0 text-aside text-faint">{n}</span>
      <span className="min-w-0 flex-1 break-keep text-pretty">{children}</span>
    </li>
  );
}

/** The screen's place while the nights are read: the figure, the band and the diary's rows, as still blocks. */
function Placeholder() {
  return (
    <div>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      <div aria-hidden>
        <div className="h-4 w-12 rounded bg-well" />
        <div className="mt-3 h-9 w-52 rounded-lg bg-well" />
        <div className="mt-3 h-4 w-40 rounded bg-well" />
        <div className="mt-7 h-8 w-full rounded-md bg-well" />
        <div className="mt-3.5 h-4 w-3/4 rounded bg-well" />
        <div className="mt-14 space-y-3">
          {[0, 1, 2, 3, 4, 5, 6].map((i) => (
            <div key={i} className="ms-[4.25rem] h-3 rounded-full bg-well" style={{ width: `${48 + ((i * 7) % 5) * 4}%` }} />
          ))}
        </div>
      </div>
    </div>
  );
}
