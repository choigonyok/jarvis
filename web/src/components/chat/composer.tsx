"use client";

import { memo, useEffect, useRef, useState, type ReactNode, type RefObject } from "react";
import { ArrowUp, ImagePlus, Mic, Reply, X } from "lucide-react";
import {
  Attachment,
  AttachmentAction,
  AttachmentContent,
  AttachmentDescription,
  AttachmentGroup,
  AttachmentMedia,
  AttachmentTitle,
} from "@/components/ui/attachment";
import { Spinner } from "@/components/ui/spinner";
import { uploadPhoto } from "@/lib/uploads";
import { cn } from "@/lib/utils";

/** One photo on its way in: shown from memory at once, sent by name once uploaded. */
type Photo = { key: string; preview: string; name?: string; error?: string };

const MAX_PHOTOS = 12;

/** The earlier message the next one will quote, as the composer shows it. */
export type Quoting = { label: string; text: string };

/** A round control the height of the bar's inner track, like a tab bar slot. */
const round =
  "tap flex size-12 shrink-0 items-center justify-center rounded-full outline-none transition-colors focus-visible:ring-3 focus-visible:ring-ring/50";

/**
 * What was typed and not sent, kept on this device. The chat page unmounts
 * when another tab is opened, and the field's state went with it - a message
 * half written was gone on coming back. Storage that throws (private mode,
 * blocked) just forgets.
 */
const draft = {
  get(key: string): string {
    try {
      return localStorage.getItem(key) ?? "";
    } catch {
      return "";
    }
  },
  set(key: string, text: string) {
    try {
      if (text.trim()) localStorage.setItem(key, text);
      else localStorage.removeItem(key);
    } catch {}
  },
};

function ComposerView({
  onSend,
  onVoice,
  onFocusChange,
  allowPhotos = false,
  photoHint = "사진에 대해 한마디 (예: 중고나라에 올려줘)",
  quoting,
  onCancelQuote,
  inputRef,
  queued,
  busy = false,
  onStop,
  draftKey,
}: {
  /** Resolves false when the message did not go out, so what was typed comes back. */
  onSend: (text: string, images: string[]) => Promise<boolean> | void;
  /** Switches to voice. Only offered where the toggle below is hidden. */
  onVoice?: () => void;
  onFocusChange?: (focused: boolean) => void;
  allowPhotos?: boolean;
  /** What the field says once a photo is attached. */
  photoHint?: string;
  /** Set while a swiped message waits to be quoted by the next one. */
  quoting?: Quoting | null;
  onCancelQuote?: () => void;
  /** The text field, for whoever needs to focus it in the same gesture. */
  inputRef?: RefObject<HTMLTextAreaElement | null>;
  /** What was sent while Jarvis works, shown at the top of this glass. */
  queued?: ReactNode;
  /** Jarvis is working: what is sent now waits its turn, and the field says so. */
  busy?: boolean;
  /** Stops the answer being written: offered in the send button's place while nothing is typed. */
  onStop?: () => void;
  /** Where what is typed but not sent is kept, so leaving the tab does not lose it. */
  draftKey?: string;
}) {
  const [value, setValue] = useState("");
  // Text only: a photo's preview is an object URL that does not outlive the page.
  const type = (text: string) => {
    setValue(text);
    if (draftKey) draft.set(draftKey, text);
  };

  // Brought back after mount, not in useState's initializer: the server renders
  // an empty field, and the first client render has to match it.
  useEffect(() => {
    if (!draftKey) return;
    const kept = draft.get(draftKey);
    // Once, from storage the server cannot see; the rule cannot tell that apart.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    if (kept) setValue((cur) => cur || kept);
  }, [draftKey]);
  const [photos, setPhotos] = useState<Photo[]>([]);
  const ownRef = useRef<HTMLTextAreaElement>(null);
  const areaRef = inputRef ?? ownRef;
  const fileRef = useRef<HTMLInputElement>(null);
  // What last pressed the send button. A click does not reliably say: Safari's
  // has no pointerType.
  const pressedBy = useRef("");

  useEffect(() => {
    const el = areaRef.current;
    if (!el) return;
    el.style.height = "auto";
    // Five lines on a phone would leave the transcript a strip; the cap is
    // lower where the screen is shorter.
    const cap = window.innerWidth < 640 ? 112 : 168;
    el.style.height = `${Math.min(el.scrollHeight, cap)}px`;
  }, [value, areaRef]);

  const uploading = photos.some((p) => !p.name && !p.error);
  const ready = photos.filter((p) => p.name);
  const canSend = !uploading && (value.trim() !== "" || ready.length > 0);
  // Something is on its way out: on a phone the send button takes the mic's
  // place, as in every messenger, and the words get the width the second
  // button held.
  const composing = value.trim() !== "" || photos.length > 0;
  // While Jarvis writes and nothing is typed, the send button's place stops
  // him - the grammar of every assistant. Typing turns it back into send:
  // what is sent then waits its turn.
  const stoppable = busy && !composing && Boolean(onStop);
  // Pressed once, it waits for the answer to end; the next answer can be
  // stopped again.
  const [stopping, setStopping] = useState(false);
  const [wasBusy, setWasBusy] = useState(busy);
  if (busy !== wasBusy) {
    setWasBusy(busy);
    if (!busy) setStopping(false);
  }

  // Escape lets go of a quote, the way it closes anything else that is open.
  useEffect(() => {
    if (!quoting) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onCancelQuote?.();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [quoting, onCancelQuote]);

  function attach(files: FileList | null) {
    if (!files) return;
    const room = MAX_PHOTOS - photos.length;
    const picked = Array.from(files).slice(0, Math.max(0, room));
    const added = picked.map((file) => ({
      key: `${Date.now()}-${Math.random()}`,
      preview: URL.createObjectURL(file),
      file,
    }));
    setPhotos((cur) => [...cur, ...added.map(({ key, preview }) => ({ key, preview }))]);
    for (const a of added) {
      uploadPhoto(a.file)
        .then((name) => setPhotos((cur) => cur.map((p) => (p.key === a.key ? { ...p, name } : p))))
        .catch((e: Error) =>
          setPhotos((cur) => cur.map((p) => (p.key === a.key ? { ...p, error: e.message } : p))),
        );
    }
  }

  function remove(key: string) {
    setPhotos((cur) => {
      const gone = cur.find((p) => p.key === key);
      if (gone) URL.revokeObjectURL(gone.preview);
      return cur.filter((p) => p.key !== key);
    });
  }

  async function send({ closeKeyboard = false } = {}) {
    if (!canSend) return;
    const text = value.trim();
    const sent = photos;
    type("");
    setPhotos([]);
    // A thumb on the button is done typing: the keyboard goes, and the answer
    // gets the screen. Enter and a mouse keep the field, the next line follows.
    if (closeKeyboard) areaRef.current?.blur();
    else areaRef.current?.focus();
    const ok = await onSend(text, sent.filter((p) => p.name).map((p) => p.name!));
    if (ok === false) {
      type(text);
      setPhotos(sent);
      return;
    }
    for (const p of sent) URL.revokeObjectURL(p.preview);
  }

  // The tab bar's own shape, so the two read as one family: a pill the bar's
  // height (3.75rem) on a single line, round controls the size of a tab slot.
  // Solid, not glass: glass is kept for what floats over the page. It grows
  // from there, keeping the radius, as a message gets long.
  // data-lead tells what sits left of the text - the photo button or only
  // padding - so a queued line above can start where the typing starts.
  return (
    <div className="composer group/composer rounded-[1.875rem] bg-popover p-1.5 transition-shadow focus-within:ring-1 focus-within:ring-edge" data-lead={allowPhotos ? "icon" : "text"}>
      {queued}
      {quoting ? (
        <div className="flex items-start gap-2 pt-1.5 pr-1 pb-1 pl-3.5">
          <Reply aria-hidden className="mt-[3px] size-3.5 shrink-0 text-faint" />
          <div className="min-w-0 flex-1">
            <p className="text-[12px] leading-snug text-dim">{quoting.label}</p>
            <p className="truncate text-[12.5px] leading-snug text-faint">{quoting.text}</p>
          </div>
          <button
            type="button"
            onClick={onCancelQuote}
            aria-label="참조 빼기"
            className="flex size-8 shrink-0 items-center justify-center rounded-full text-faint outline-none hover:text-foreground focus-visible:ring-3 focus-visible:ring-ring/50"
          >
            <X aria-hidden className="size-4" />
          </button>
        </div>
      ) : null}

      {photos.length > 0 ? (
        <AttachmentGroup role="list" aria-label="첨부한 사진" className="snap-none scroll-fade-none mb-1 gap-1.5 px-2.5 pt-1.5 pb-0">
          {photos.map((p) => {
            const state = p.error ? "error" : p.name ? "done" : "uploading";
            return (
              <Attachment key={p.key} role="listitem" surface="bare" size="photo" state={state} className="min-w-0 flex-nowrap">
                <div className="relative shrink-0">
                  <AttachmentMedia
                    variant="image"
                    className="size-16 w-16 rounded-2xl bg-transparent transition-opacity group-data-[state=error]/attachment:opacity-30 group-data-[state=error]/attachment:grayscale group-data-[state=uploading]/attachment:opacity-45"
                  >
                    {/* eslint-disable-next-line @next/next/no-img-element -- a local object URL, nothing to optimise */}
                    <img src={p.preview} alt="" />
                  </AttachmentMedia>
                  {state === "uploading" ? (
                    <span className="absolute inset-0 flex items-center justify-center">
                      <Spinner aria-label="사진을 올리는 중" className="text-foreground/80" />
                    </span>
                  ) : null}
                  <AttachmentAction
                    onClick={() => remove(p.key)}
                    aria-label="사진 빼기"
                    className="absolute -top-0.5 -right-0.5 size-6 rounded-full bg-background/90 text-dim hover:bg-background/90 hover:text-foreground"
                  >
                    <X aria-hidden className="size-3.5" />
                  </AttachmentAction>
                </div>
                {/* Why it failed, beside it: a phone has no pointer to hover for it. */}
                {p.error ? (
                  <AttachmentContent className="w-36 pr-1">
                    <AttachmentTitle className="font-normal text-reject">올리지 못했어요</AttachmentTitle>
                    <AttachmentDescription className="line-clamp-2 text-[11.5px] leading-snug whitespace-normal text-dim group-data-[state=error]/attachment:text-dim">
                      {p.error}
                    </AttachmentDescription>
                  </AttachmentContent>
                ) : null}
              </Attachment>
            );
          })}
        </AttachmentGroup>
      ) : null}

      <div className="flex items-end">
        {allowPhotos ? (
          <>
            <input
              ref={fileRef}
              type="file"
              accept="image/*"
              multiple
              hidden
              onChange={(e) => {
                attach(e.target.files);
                e.target.value = "";
              }}
            />
            <button
              type="button"
              onClick={() => fileRef.current?.click()}
              disabled={photos.length >= MAX_PHOTOS}
              aria-label="사진 첨부"
              className={cn(round, "text-faint hover:text-foreground disabled:opacity-40")}
            >
              <ImagePlus aria-hidden className="size-5" strokeWidth={1.8} />
            </button>
          </>
        ) : null}

        <textarea
          ref={areaRef}
          rows={1}
          value={value}
          onChange={(e) => type(e.target.value)}
          onFocus={() => onFocusChange?.(true)}
          onBlur={() => onFocusChange?.(false)}
          onKeyDown={(e) => {
            // Enter on a phone keyboard is a newline, not a send: there is no
            // shift to hold, and a stray send is worse than a stray line.
            if (
              e.key === "Enter" &&
              !e.shiftKey &&
              !e.nativeEvent.isComposing &&
              window.innerWidth >= 640
            ) {
              e.preventDefault();
              void send();
            }
          }}
          placeholder={
            quoting
              ? "이걸로 무엇을 할까요?"
              : photos.length
                ? photoHint
                : busy
                  ? "끝나면 이어서 보낼 말"
                  : "Jarvis에게 메시지"
          }
          aria-label="Jarvis에게 보낼 메시지"
          // What is typed here is kept (draftKey): the page may be reloaded under it.
          data-keeps-draft={draftKey ? "" : undefined}
          className={cn(
            "scrollbar-hairline max-h-28 min-h-12 flex-1 resize-none bg-transparent py-3 text-[14.5px] leading-relaxed text-foreground outline-none placeholder:text-faint sm:max-h-[168px]",
            allowPhotos ? "px-1" : "pl-3.5 pr-1",
          )}
        />

        {/* The mode switch lives here on a phone. Stacking a separate toggle
            under the composer would put three bars between the transcript and
            the bottom of the screen; a mic in the send button's place while
            nothing is typed is the grammar every messaging app already taught. */}
        {onVoice && !composing && !stoppable ? (
          <button
            type="button"
            onClick={onVoice}
            aria-label="음성으로 말하기"
            className={cn(round, "text-faint hover:text-foreground sm:hidden")}
          >
            <Mic aria-hidden className="size-5" strokeWidth={1.8} />
          </button>
        ) : null}

        {stoppable ? (
          // Quiet like send, and not red: stopping is not turning anything down.
          <button
            type="button"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => {
              setStopping(true);
              onStop?.();
            }}
            disabled={stopping}
            aria-label={stopping ? "멈추는 중" : "답 멈추기"}
            title="답 멈추기"
            className={cn(round, "group text-dim disabled:opacity-40")}
          >
            <span className="flex size-9 items-center justify-center rounded-full bg-glass-raised transition-colors group-enabled:group-hover:text-foreground">
              <span aria-hidden className="size-2.5 rounded-[2px] bg-current" />
            </span>
          </button>
        ) : (
        /* Deliberately quiet: the brightest control in this product is 승인. */
        <button
          type="button"
          onPointerDown={(e) => {
            pressedBy.current = e.pointerType;
          }}
          // Not taking the focus from the field on the way down: without it
          // the tab bar comes back under the finger, this button moves up, and
          // the press lands on whatever is there now. send() lets go of the
          // field once the message is out.
          onMouseDown={(e) => e.preventDefault()}
          onClick={() => {
            void send({ closeKeyboard: pressedBy.current === "touch" || pressedBy.current === "pen" });
            pressedBy.current = "";
          }}
          disabled={!canSend}
          aria-label={uploading ? "사진을 올리는 중" : "메시지 보내기"}
          className={cn(round, "group text-dim disabled:opacity-40", onVoice && !composing && "max-sm:hidden")}
        >
          <span className="flex size-9 items-center justify-center rounded-full bg-glass-raised transition-colors group-enabled:group-hover:text-foreground">
            <ArrowUp aria-hidden className="size-[18px]" />
          </span>
        </button>
        )}
      </div>
    </div>
  );
}

// Drawn again only when what they show changes: the thread above them
// renders on every frame of an answer.
export const Composer = memo(ComposerView);
