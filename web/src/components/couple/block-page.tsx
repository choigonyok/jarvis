"use client";

import { BucketPage } from "@/components/couple/bucket";
import { DateMapPage } from "@/components/couple/date-map";
import { DDayPage } from "@/components/couple/dday";
import { FreePage } from "@/components/couple/free";
import { HabitPage } from "@/components/couple/habit";
import { CoupleScreen } from "@/components/couple/screen";
import { useBlock, useSpace } from "@/components/couple/use-space";
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
  const failed = (block.error && !block.data) || (space.error && !space.data);
  const b = block.data;

  return (
    <CoupleScreen back>
      {failed ? (
        <Failed onRetry={reload} className="mt-2">
          {block.error?.message === "404" || /없습니다/.test(block.error?.message ?? "") ? "이 블록이 없어요. 지워졌을 수 있어요." : SAY.couldNotLoad}
        </Failed>
      ) : !b || !space.data ? (
        <p className="pt-10 text-center text-ui text-faint">{SAY.loading}</p>
      ) : b.type === "dday" ? (
        <DDayPage block={b} space={space.data} onChanged={reload} />
      ) : b.type === "bucket" ? (
        <BucketPage block={b} onChanged={reload} />
      ) : b.type === "workout" ? (
        <HabitPage block={b} space={space.data} />
      ) : b.type === "map" ? (
        <DateMapPage block={b} onChanged={reload} />
      ) : b.type === "free" ? (
        <FreePage block={b} space={space.data} onChanged={reload} />
      ) : (
        <div>
          <h1 className="text-display leading-[1.2] font-semibold tracking-tight text-foreground">{b.title}</h1>
          <p className="mt-3 text-ui text-dim">{BLOCK_NAME[b.type] ?? "이 블록"}은 uniple 앱에서 열어요.</p>
        </div>
      )}
    </CoupleScreen>
  );
}
