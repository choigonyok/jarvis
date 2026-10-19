"use client";

import { useEffect, useState, type ReactNode } from "react";
import { Header, TabBar } from "@/components/shell/header";
import { StandingBar } from "@/components/shell/standing-bar";
import { Meals } from "@/components/life/meals";
import { Notes } from "@/components/life/notes";
import {
  type Loaded,
  type Meal,
  type Note,
  type Place,
  type Thing,
  byAttention,
  byNeed,
  daysUntil,
  dueLabel,
  read,
  shortDate,
} from "@/lib/life";
import { krw } from "@/lib/portfolio";
import { isPending } from "@/lib/thread";
import { useThread } from "@/lib/use-thread";
import { cn } from "@/lib/utils";

type Book = {
  notes: Loaded<Note[]>;
  places: Loaded<Place[]>;
  things: Loaded<Thing[]>;
  meals: Loaded<Meal[]>;
};

/**
 * What Jarvis has come to know about the operator's life (mind): the notes
 * it believes about them, waiting for a yes or a no, and the ledgers it
 * reads from their days - the places they reviewed, the things they keep
 * buying, the meals they paid for.
 *
 * The screen opens on the question that brings someone here - is there
 * anything it wants me to check - and every line below is the status tab's
 * one line: a name, a figure on the right, a line of what it means.
 * Monthly charges are not here: 가계부's 매달 나가는 돈 is the same list.
 */
export function Life() {
  const { proposals, connection } = useThread();
  const waiting = proposals.filter(isPending);
  const [book, setBook] = useState<Book | null>(null);

  useEffect(() => {
    let live = true;
    void Promise.all([
      read<Note[]>("notes", "notes"),
      read<Place[]>("places", "places"),
      read<Thing[]>("things", "things"),
      read<Meal[]>("meals?days=30", "meals"),
    ]).then(([notes, places, things, meals]) => {
      if (!live) return;
      // The order is the visit's: an answered note keeps its place until next time.
      if (notes.data) notes = { data: byAttention(notes.data) };
      setBook({ notes, places, things, meals });
    });
    return () => {
      live = false;
    };
  }, []);

  function changed(next: Note) {
    setBook((b) => (b?.notes.data ? { ...b, notes: { data: b.notes.data.map((n) => (n.id === next.id ? next : n)) } } : b));
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <Header connection={connection} pending={waiting.length} />
      <StandingBar pending={waiting} href="/record" />

      <main className="scrollbar-hairline inset-x-safe min-h-0 flex-1 overflow-y-auto overscroll-contain pb-tabbar">
        <div className="mx-auto w-full max-w-[42rem] px-4 pt-6 pb-12 sm:px-8 sm:pt-10">
          {!book ? (
            <p className="pt-10 text-center text-[13px] text-faint">불러오는 중이에요.</p>
          ) : [book.notes, book.places, book.things, book.meals].every((x) => x.error) ? (
            // Nothing came back: one sentence for it, not four of the same.
            <div>
              <p className="text-[26px] leading-[1.25] font-medium tracking-[-0.015em] text-foreground sm:text-[30px]">
                생활 장부를 불러오지 못했어요
              </p>
              <p role="alert" className="mt-2 max-w-[34rem] text-[13.5px] leading-[1.6] break-keep text-pretty text-dim">
                {book.notes.error} 잠시 뒤 다시 열어 보세요. 계속 이러면 상태 탭에서 인지 루프(mind)가 응답하는지 보세요.
              </p>
            </div>
          ) : (
            <>
              <Headline notes={book.notes} />

              <Section name="나에 대한 노트" focus="notes" tally={noteTally(book.notes.data)}>
                {book.notes.error ? (
                  <Trouble what="노트를">{book.notes.error}</Trouble>
                ) : book.notes.data!.length === 0 ? (
                  <Empty>밤마다 반복되는 일과 빗나간 예측에서 노트가 생겨요.</Empty>
                ) : (
                  <Notes notes={book.notes.data!} onChange={changed} />
                )}
              </Section>

              <Section name="나만의 지도" focus="places" tally={book.places.data?.length ? `${book.places.data.length}곳` : null}>
                {book.places.error ? (
                  <Trouble what="지도를">{book.places.error}</Trouble>
                ) : book.places.data!.length === 0 ? (
                  <Empty>캠핑이나 처음 간 식당 뒤에 오는 회고 카드에 답하면 그곳이 여기 쌓여요.</Empty>
                ) : (
                  <Places places={book.places.data!} />
                )}
              </Section>

              <Section name="가진 물건" focus="things" tally={book.things.data?.length ? `반년 ${book.things.data.length}종` : null}>
                {book.things.error ? (
                  <Trouble what="물건을">{book.things.error}</Trouble>
                ) : book.things.data!.length === 0 ? (
                  <Empty>쿠팡 주문이 상품별로 읽히면 아침마다 조금씩 종류가 나뉘어 여기 모여요.</Empty>
                ) : (
                  <Things things={book.things.data!} />
                )}
              </Section>

              <Section name="식사" focus="meals" tally="최근 30일">
                {book.meals.error ? <Trouble what="식사를">{book.meals.error}</Trouble> : <Meals meals={book.meals.data!} />}
              </Section>
            </>
          )}
        </div>
      </main>

      <TabBar pending={waiting.length} />
    </div>
  );
}

function Headline({ notes }: { notes: Loaded<Note[]> }) {
  const list = notes.data ?? [];
  const asking = list.filter((n) => n.status === "hypothesis").length;
  const sentence = notes.error
    ? "노트를 불러오지 못했어요"
    : asking > 0
      ? `확인할 노트가 ${asking}개 있어요`
      : list.length > 0
        ? `노트 ${list.length}개를 모두 확인했어요`
        : "아직 노트가 없어요";
  return (
    <div>
      <p className="text-[26px] leading-[1.25] font-medium tracking-[-0.015em] text-foreground sm:text-[30px]">{sentence}</p>
      <p className="mt-2 max-w-[34rem] text-[13.5px] leading-[1.6] break-keep text-pretty text-dim">
        Jarvis가 지난 일들에서 나에 대해 짐작한 문장이에요. 맞다고 한 것은 예측과 제안에 그대로 쓰고, 아니라고 한 것은
        다시 만들지 않아요.
      </p>
    </div>
  );
}

function noteTally(notes: Note[] | undefined): string | null {
  if (!notes?.length) return null;
  const n = (s: Note["status"]) => notes.filter((x) => x.status === s).length;
  return [n("hypothesis") && `확인 전 ${n("hypothesis")}`, n("confirmed") && `맞음 ${n("confirmed")}`, n("rejected") && `아님 ${n("rejected")}`]
    .filter(Boolean)
    .join(" · ");
}

/** A part of the screen: its name, and on the right what is in it. */
function Section({ name, focus, tally, children }: { name: string; focus: string; tally: string | null; children: ReactNode }) {
  const id = `life-${focus}`;
  return (
    <section className="mt-10 sm:mt-12" aria-labelledby={id} data-focus={focus}>
      <div className="mb-1 flex items-baseline justify-between gap-3">
        <h2 id={id} className="text-[12px] text-faint">
          {name}
        </h2>
        {tally ? <span className="tnum text-[11.5px] text-faint">{tally}</span> : null}
      </div>
      {children}
    </section>
  );
}

function Empty({ children }: { children: ReactNode }) {
  return <p className="py-3 text-[13px] leading-[1.55] break-keep text-pretty text-faint">{children}</p>;
}

/** One part that did not come back, while the others did. */
function Trouble({ what, children }: { what: string; children: ReactNode }) {
  return (
    <p role="alert" className="py-3 text-[13px] leading-[1.55] break-keep text-pretty text-dim">
      {what} 불러오지 못했어요. {children} 잠시 뒤 다시 열어 보세요.
    </p>
  );
}

/** The places reviewed, best first: how they scored, how often, what they cost, what they are known for. */
function Places({ places }: { places: Place[] }) {
  return (
    <ul className="divide-y divide-edge-soft">
      {places.map((p) => {
        const said = [
          p.type,
          p.visits > 0 && `${p.visits}번 다녀감`,
          p.spent > 0 && krw(p.spent),
          `회고 ${p.reviews}번`,
        ].filter(Boolean);
        return (
          <li key={`${p.type}:${p.name}`} className="py-3">
            <div className="flex items-baseline justify-between gap-3">
              <span className="min-w-0 truncate text-[14.5px] text-foreground/90">{p.name}</span>
              <span className="tnum shrink-0 text-[13px] text-foreground/90">
                {p.score.toFixed(1)}
                <span className="text-[11.5px] text-faint"> / 5</span>
              </span>
            </div>
            <p className="mt-0.5 text-[12.5px] text-faint">
              {said.join(" · ")} · 마지막 {shortDate(p.last)}
            </p>
            {p.good?.length || p.bad?.length ? (
              <p className="mt-1 text-[12.5px] leading-[1.55] text-pretty">
                {p.good?.length ? (
                  <span className="text-dim">
                    <span className="text-faint">좋았던 점 </span>
                    {p.good.join(", ")}
                  </span>
                ) : null}
                {p.good?.length && p.bad?.length ? <span className="text-faint"> · </span> : null}
                {p.bad?.length ? (
                  <span className="text-dim">
                    <span className="text-faint">아쉬웠던 점 </span>
                    {p.bad.join(", ")}
                  </span>
                ) : null}
              </p>
            ) : null}
          </li>
        );
      })}
    </ul>
  );
}

const OTHERS_SHOWN = 6;

/**
 * What was bought in the last half year, by kind. What keeps coming round
 * first, by when it runs out; then the rest, folded. The sizes the names
 * said come first of all - the line wanted when buying the next pair.
 */
function Things({ things }: { things: Thing[] }) {
  const [more, setMore] = useState(false);
  const ordered = byNeed(things);
  const cycling = ordered.filter((t) => t.next);
  const others = ordered.filter((t) => !t.next);
  const sizes = things.filter((t) => t.sizes?.length);

  return (
    <div>
      {sizes.length > 0 ? (
        <p className="py-2 text-[13px] leading-[1.6] text-pretty text-dim">
          <span className="text-faint">사이즈 </span>
          {sizes.map((t, i) => (
            <span key={t.class}>
              {i > 0 ? <span className="text-faint"> · </span> : null}
              {t.class} <span className="tnum text-foreground/90">{t.sizes!.join(", ")}</span>
            </span>
          ))}
        </p>
      ) : null}

      <ul className="divide-y divide-edge-soft">
        {cycling.map((t) => {
          const left = daysUntil(t.next!);
          return (
            <li key={t.class} className="py-3">
              <div className="flex items-baseline justify-between gap-3">
                <span className="min-w-0 truncate text-[14.5px] text-foreground/90">{t.class}</span>
                <span className={cn("tnum shrink-0 text-[13px]", left <= 2 ? "text-foreground" : "text-faint")}>
                  {left <= 0 ? "살 때 됐어요" : `${dueLabel(t.next!)} 차례`}
                </span>
              </div>
              <p className="mt-0.5 text-[12.5px] text-faint">
                보통 {t.cycleDays}일마다 · 마지막 {shortDate(t.bought[t.bought.length - 1] ?? t.last)} · 반년에 {t.bought.length}번
              </p>
              <p className="mt-0.5 truncate text-[12.5px] text-dim">{t.items.join(", ")}</p>
            </li>
          );
        })}
        {(more ? others : others.slice(0, OTHERS_SHOWN)).map((t) => (
          <li key={t.class} className="py-3">
            <div className="flex items-baseline justify-between gap-3">
              <span className="min-w-0 truncate text-[14.5px] text-foreground/80">{t.class}</span>
              <span className="tnum shrink-0 text-[11.5px] text-faint">
                {shortDate(t.bought[t.bought.length - 1] ?? t.last)}
                {t.bought.length > 1 ? ` · ${t.bought.length}번` : ""}
              </span>
            </div>
            <p className="mt-0.5 truncate text-[12.5px] text-faint">{t.items.join(", ")}</p>
          </li>
        ))}
      </ul>
      {!more && others.length > OTHERS_SHOWN ? (
        <button
          type="button"
          onClick={() => setMore(true)}
          className="tap mt-1 text-[12.5px] text-dim outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
        >
          {others.length - OTHERS_SHOWN}종 더 보기
        </button>
      ) : null}
    </div>
  );
}
