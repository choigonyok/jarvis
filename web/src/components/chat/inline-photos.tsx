"use client";

import { createContext, Fragment, useContext, useMemo, useState, type ReactNode } from "react";
import { Lightbox } from "@/components/ui/lightbox";
import { photoUrl } from "@/lib/uploads";
import { cn } from "@/lib/utils";

/**
 * A photo of the agent's own named in words - a photo the operator sent, a
 * screen a job took - by the name the agent keeps it under (agent
 * internal/uploads: 32 hex digits and the kind of picture). A job that took
 * 32 screens listed them by name in its last word, brand by brand, and the
 * card showed 32 file names (2026-11-07).
 *
 * Written anywhere, a name is shown as a numbered mark where it stands - "사진
 * 1" - and the photo under the line or the paragraph it is in, with the same
 * number: the words keep their shape, and each photo stays beside what the
 * words say of it. Drawn inside the line at first, the photos tangled the
 * words around them (2026-11-07).
 */
export const UPLOAD_NAME = /\b[0-9a-f]{32}\.(?:jpg|png|webp)\b/g;

/**
 * The photos some words name, in order, each once - not a name inside code,
 * which stays the literal it is (chat/markdown.tsx).
 */
export function uploadsIn(text: string): string[] {
  const words = text.replace(/```[\s\S]*?```/g, "").replace(/`[^`\n]*`/g, "");
  return [...new Set(words.match(UPLOAD_NAME) ?? [])];
}

/** The name a link to one of the agent's photos is to; null for any other address. */
export function uploadOf(src: string): string | null {
  const m = /^\/api\/agent\/uploads\/([0-9a-f]{32}\.(?:jpg|png|webp))$/.exec(src);
  return m && photoUrl(m[1]) === src ? m[1] : null;
}

type Album = { names: string[]; gone: Set<string>; open: (name: string) => void };

const AlbumContext = createContext<Album | null>(null);

/**
 * The photos a message names: what numbers them, and the full-screen view
 * each opens in - among the rest, but for one that could not be read.
 */
export function UploadAlbum({ names, children }: { names: string[]; children: ReactNode }) {
  const [gone] = useState(() => new Set<string>());
  const [at, setAt] = useState<string | null>(null);
  // The names by what they are, not by the array they came in: a message
  // drawn again names the same photos.
  const key = names.join("|");
  const album = useMemo(() => ({ names: key ? key.split("|") : [], gone, open: setAt }), [key, gone]);
  const shown = album.names.filter((n) => n === at || !gone.has(n));
  return (
    <AlbumContext.Provider value={album}>
      {children}
      {at ? (
        <Lightbox
          items={shown.map((n) => ({ src: photoUrl(n), alt: `사진 ${album.names.indexOf(n) + 1}` }))}
          start={Math.max(0, shown.indexOf(at))}
          onClose={() => setAt(null)}
        />
      ) : null}
    </AlbumContext.Provider>
  );
}

/** A photo's number in its message, from 1; 0 outside one. */
function useNumber(name: string): [number, Album | null] {
  const album = useContext(AlbumContext);
  return [album ? album.names.indexOf(name) + 1 : 0, album];
}

/** Where a photo is named in the words: its number, which opens it. */
export function UploadMark({ name }: { name: string }) {
  const [n, album] = useNumber(name);
  if (!album || n === 0) return <>사진</>;
  return (
    <button
      type="button"
      onClick={() => album.open(name)}
      aria-label={`사진 ${n} 크게 보기`}
      className="mx-px inline rounded-sm bg-glass-raised px-1 py-px align-baseline text-[0.92em] whitespace-nowrap text-foreground focus-ring"
    >
      사진 {n}
    </button>
  );
}

/** The photos a line or a paragraph names, under it, each with its number. */
export function UploadRow({ names, small }: { names: string[]; small?: boolean }) {
  return (
    <span className={cn("flex flex-wrap gap-1.5", small ? "mt-1.5 mb-1" : "my-1.5")}>
      {names.map((name) => (
        <UploadTile key={name} name={name} small={small} />
      ))}
    </span>
  );
}

/**
 * One photo in a row: tapped, it opens full screen among the others of its
 * message. One deleted since - a job's go some days after it ends - is an
 * empty tile that says so.
 */
function UploadTile({ name, small }: { name: string; small?: boolean }) {
  const [n, album] = useNumber(name);
  const [gone, setGone] = useState(false);
  if (gone) {
    return (
      <span className={cn("flex items-center justify-center rounded-md bg-well px-2 text-meta text-faint", small ? "h-16" : "h-20")}>
        {n ? `사진 ${n} · ` : ""}지운 사진
      </span>
    );
  }
  return (
    <button
      type="button"
      onClick={() => album?.open(name)}
      aria-label={n ? `사진 ${n} 크게 보기` : "사진 크게 보기"}
      className="relative block overflow-hidden rounded-md bg-well focus-ring"
    >
      {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
      <img
        src={photoUrl(name)}
        alt={n ? `사진 ${n}` : "사진"}
        loading="lazy"
        onError={() => {
          album?.gone.add(name);
          setGone(true);
        }}
        className={cn("block w-auto object-cover", small ? "h-16 min-w-16 max-w-28" : "h-20 min-w-20 max-w-36")}
      />
      {/* Its edge: a screen of a white page on the light theme was a white
          square on white. */}
      <span aria-hidden className="pointer-events-none absolute inset-0 rounded-md ring-1 ring-edge ring-inset" />
      {n ? (
        <span aria-hidden className="tnum absolute top-1 left-1 rounded-sm bg-black/60 px-1 text-tiny leading-4 text-white">
          {n}
        </span>
      ) : null}
    </button>
  );
}

/**
 * Plain words with the photos they name - a job's line in the 작업 tab:
 * each line as it is, a name in it its numbered mark, and the line's photos
 * in a row under it.
 */
export function WithUploads({ text }: { text: string }) {
  const names = uploadsIn(text);
  if (names.length === 0) return <>{text}</>;
  return (
    <UploadAlbum names={names}>
      {text.split("\n").map((line, i) => {
        const here = [...new Set(line.match(UPLOAD_NAME) ?? [])];
        const parts: ReactNode[] = [];
        let last = 0;
        for (const m of line.matchAll(UPLOAD_NAME)) {
          parts.push(line.slice(last, m.index));
          parts.push(<UploadMark key={m.index} name={m[0]} />);
          last = m.index + m[0].length;
        }
        parts.push(line.slice(last));
        return (
          <Fragment key={i}>
            <span className="block min-h-[1lh]">{parts}</span>
            {here.length ? <UploadRow names={here} small /> : null}
          </Fragment>
        );
      })}
    </UploadAlbum>
  );
}
