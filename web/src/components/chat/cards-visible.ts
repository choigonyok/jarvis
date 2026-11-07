"use client";

import { useEffect, useState, type RefObject } from "react";
import type { ChatMode } from "@/components/chat/mode-toggle";
import type { Proposal, Turn } from "@/lib/thread";

/**
 * Whether a waiting card is on screen in the conversation. The bar over the
 * thread speaks for what you cannot see; a card sitting in the transcript in
 * front of you is already saying it.
 */
export function useCardsVisible(
  scrollRef: RefObject<HTMLDivElement | null>,
  {
    pending,
    turns,
    proposals,
    mode,
  }: {
    /** How many cards wait. */
    pending: number;
    /** The rows, and the cards in them, come and go with these. */
    turns: Turn[];
    proposals: Proposal[];
    /** The voice stage builds the scroller anew on its return. */
    mode: ChatMode;
  },
): boolean {
  const [cardsVisible, setCardsVisible] = useState(false);

  // Watch the waiting cards themselves rather than guessing from scroll
  // position: the bar exists to speak for a decision you cannot see, so what
  // it needs to know is literally whether one is on screen.
  useEffect(() => {
    const root = scrollRef.current;
    if (!root || pending === 0) {
      setCardsVisible(false);
      return;
    }
    const cards = root.querySelectorAll("[data-pending-card]");
    if (cards.length === 0) {
      setCardsVisible(false);
      return;
    }
    const seen = new Set<Element>();
    // Under the glass top or under the composer a card is not one you can read.
    const style = getComputedStyle(root);
    const inset = (name: string) => Math.round(parseFloat(style.getPropertyValue(name)) || 0);
    const observer = new IntersectionObserver(
      (entries) => {
        for (const entry of entries) {
          if (entry.isIntersecting) seen.add(entry.target);
          else seen.delete(entry.target);
        }
        setCardsVisible(seen.size > 0);
      },
      { root, threshold: 0.35, rootMargin: `-${inset("--top-space")}px 0px -${inset("--footer-space")}px 0px` },
    );
    for (const card of cards) observer.observe(card);
    return () => observer.disconnect();
  }, [pending, turns, proposals, mode, scrollRef]);

  return cardsVisible;
}
