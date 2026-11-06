"use client";

import { createContext, useContext, useMemo, useState, type ReactNode } from "react";
import { Lightbox } from "@/components/ui/lightbox";
import { photoUrl } from "@/lib/uploads";

/**
 * A photo of the agent's own named in words - a photo the operator sent, a
 * screen a job took - by the name the agent keeps it under (agent
 * internal/uploads: 32 hex digits and the kind of picture). A job that took
 * 32 screens listed them by name in its last word, brand by brand, and the
 * card showed 32 file names (2026-11-07): written anywhere, a name is shown as
 * the photo, where it stands.
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

type Album = { names: string[]; gone: Set<string> };

const AlbumContext = createContext<Album>({ names: [], gone: new Set() });

/**
 * The photos a message names, so that each opens among the rest - but for
 * one that could not be read, which the full-screen view steps over.
 */
export function UploadAlbum({ names, children }: { names: string[]; children: ReactNode }) {
  const [gone] = useState(() => new Set<string>());
  // The names by what they are, not by the array they came in: a message
  // drawn again names the same photos.
  const key = names.join("|");
  const album = useMemo(() => ({ names: key ? key.split("|") : [], gone }), [key, gone]);
  return <AlbumContext.Provider value={album}>{children}</AlbumContext.Provider>;
}

/**
 * One photo named in a line, as a small tile in it: what it is a picture of
 * is what the words around it say. A tap opens it full screen, among the
 * other photos of the same message. One deleted since - a job's go some days
 * after it ends - says so instead of a broken picture.
 */
export function UploadThumb({ name }: { name: string }) {
  const album = useContext(AlbumContext);
  const [open, setOpen] = useState(false);
  const [gone, setGone] = useState(false);
  if (gone) return <span className="text-faint">(지운 사진)</span>;
  const all = album.names.includes(name) ? album.names.filter((n) => n === name || !album.gone.has(n)) : [name];
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label="사진 크게 보기"
        className="mx-0.5 inline-block overflow-hidden rounded-md bg-well align-middle focus-ring"
      >
        {/* eslint-disable-next-line @next/next/no-img-element -- served by the agent behind the proxy */}
        <img
          src={photoUrl(name)}
          alt="사진"
          loading="lazy"
          onError={() => {
            album.gone.add(name);
            setGone(true);
          }}
          className="block h-14 w-auto max-w-28 object-cover"
        />
      </button>
      {open ? (
        <Lightbox items={all.map((n) => ({ src: photoUrl(n), alt: "사진" }))} start={all.indexOf(name)} onClose={() => setOpen(false)} />
      ) : null}
    </>
  );
}

/** Plain words with the photos they name shown where they are named: a job's line in the 작업 tab. */
export function WithUploads({ text }: { text: string }) {
  const names = uploadsIn(text);
  if (names.length === 0) return <>{text}</>;
  const parts: ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(UPLOAD_NAME)) {
    parts.push(text.slice(last, m.index));
    parts.push(<UploadThumb key={m.index} name={m[0]} />);
    last = m.index + m[0].length;
  }
  parts.push(text.slice(last));
  return <UploadAlbum names={names}>{parts}</UploadAlbum>;
}
