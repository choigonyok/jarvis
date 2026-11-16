"use client";

import { AlbumPage } from "@/components/couple/album";
import { BucketPage } from "@/components/couple/bucket";
import { DateMapPage } from "@/components/couple/date-map";
import { DDayPage } from "@/components/couple/dday";
import { FreePage } from "@/components/couple/free";
import { HabitPage } from "@/components/couple/habit";
import { CoupleScreen } from "@/components/couple/screen";
import { BudgetPage, DiaryPage, FoodPage, LogPage } from "@/components/couple/shelf";
import { useBlock, useSpace } from "@/components/couple/use-space";
import { WorldMapPage } from "@/components/couple/world-page";
import { AsOf } from "@/components/shell/as-of";
import { Failed } from "@/components/shell/failed";
import { BLOCK_NAME } from "@/lib/couple";
import { SAY } from "@/lib/say";

/** One block's page, by what kind of block it is. */
export function BlockPage({ id }: { id: string }) {
  const space = useSpace();
  const block = useBlock(id);
  const reload = async () => {
    await Promise.all([block.reload(), space.reload()]);
  };
  // A block gone is gone, whatever was read of it before.
  const gone = block.error?.message === "404" || /없습니다/.test(block.error?.message ?? "");
  const failed = gone || (block.error && !block.data) || (space.error && !space.data);
  const b = block.data;

  return (
    <CoupleScreen back>
      {!failed && b && space.data && (block.error || space.error) ? (
        <AsOf at={block.error ? block.readAt : space.readAt} failed onRetry={() => void reload()} className="mb-3" />
      ) : null}
      {failed ? (
        <Failed onRetry={reload} className="mt-2">
          {gone ? "이 블록이 없어요. 지워졌을 수 있어요." : SAY.couldNotLoad}
        </Failed>
      ) : !b || !space.data ? (
        <PagePlaceholder title={space.data?.blocks.find((x) => x.id === id)?.title} />
      ) : b.type === "dday" ? (
        <DDayPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "bucket" ? (
        <BucketPage block={b} onChanged={reload} />
      ) : b.type === "workout" ? (
        <HabitPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "map" ? (
        <DateMapPage block={b} onChanged={reload} />
      ) : b.type === "free" ? (
        <FreePage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "worldmap" ? (
        <WorldMapPage block={b} onChanged={reload} />
      ) : b.type === "album" ? (
        <AlbumPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "log" ? (
        <LogPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "food" ? (
        <FoodPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "diary" ? (
        <DiaryPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "budget" ? (
        <BudgetPage block={b} space={space.data} onChanged={reload} />
      ) : (
        <div>
          <h1 className="text-display leading-[1.2] font-semibold tracking-tight text-foreground">{b.title}</h1>
          <p className="mt-3 text-ui text-dim">{BLOCK_NAME[b.type] ?? "이 블록"}은 여기서 열 수 없어요.</p>
        </div>
      )}
    </CoupleScreen>
  );
}

/**
 * A page before its block was ever read in this visit to the app: its name,
 * when 우리 has it, over quiet blocks the page arrives into.
 */
function PagePlaceholder({ title }: { title?: string }) {
  return (
    <div>
      <p role="status" className="sr-only">
        {SAY.loading}
      </p>
      {title ? (
        <h1 className="text-display leading-[1.2] font-semibold tracking-tight break-keep text-foreground">{title}</h1>
      ) : (
        <div aria-hidden className="my-1 h-(--display) w-40 rounded-lg bg-well" />
      )}
      <div aria-hidden>
        <div className="mt-5 h-4 w-48 rounded bg-well" />
        <div className="mt-5 h-48 rounded-2xl bg-glass" />
      </div>
    </div>
  );
}
