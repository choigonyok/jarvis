"use client";

import { useEffect, useRef, useState } from "react";

/**
 * How loud you are actually being, 0..1.
 *
 * The voice stage's ring moves on this rather than on a sine wave, because a
 * ring that moves when the room is silent is a decoration pretending to be a
 * readout - and the one thing this screen has to tell you is whether it can
 * hear you.
 *
 * Its own getUserMedia stream, separate from the one SpeechRecognition opens.
 * The recognizer does not expose its audio, and two readers of the same mic is
 * something browsers handle fine.
 */
export function useMicLevel(active: boolean): {
  level: number;
  /**
   * The latest frequency bins, 0..255, refreshed every frame in place. A ref
   * rather than state: the spectrum is drawn straight to a canvas, and
   * re-rendering React sixty times a second for it would be waste.
   */
  spectrum: React.RefObject<Uint8Array | null>;
} {
  const [level, setLevel] = useState(0);
  const spectrum = useRef<Uint8Array | null>(null);
  // The rendered value is smoothed; the raw peak is kept here so a loud
  // syllable rises fast and decays slowly, the way a meter reads.
  const smoothed = useRef(0);

  useEffect(() => {
    if (!active) {
      // Resetting from an effect on purpose: this mirrors React state to an
      // external device being switched off, which is what effects are for.
      // eslint-disable-next-line react-hooks/set-state-in-effect
      setLevel(0);
      smoothed.current = 0;
      spectrum.current = null;
      return;
    }

    let stop = false;
    let ctx: AudioContext | undefined;
    let stream: MediaStream | undefined;
    let frame = 0;

    async function listen() {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      } catch {
        // Denied or unavailable: the ring simply rests. The recognizer
        // reports the permission problem in words; this does not repeat it.
        return;
      }
      if (stop) {
        for (const track of stream.getTracks()) track.stop();
        return;
      }

      ctx = new AudioContext();
      const source = ctx.createMediaStreamSource(stream);
      const analyser = ctx.createAnalyser();
      // 1024 gives ~47Hz bins, fine enough to tell the low and high parts of
      // a voice apart around the spectrum ring.
      analyser.fftSize = 1024;
      analyser.smoothingTimeConstant = 0.6;
      source.connect(analyser);

      const buffer = new Uint8Array(analyser.fftSize);
      const bins = new Uint8Array(analyser.frequencyBinCount);
      spectrum.current = bins;

      const tick = () => {
        if (stop) return;
        analyser.getByteTimeDomainData(buffer);
        analyser.getByteFrequencyData(bins);
        // RMS around the 128 midpoint is the honest measure of loudness;
        // peak alone would make one click look like speech.
        let sum = 0;
        for (const sample of buffer) {
          const centered = (sample - 128) / 128;
          sum += centered * centered;
        }
        const rms = Math.sqrt(sum / buffer.length);
        // Speech sits low in this range, so it is lifted to something the eye
        // can read without making silence look like talking.
        const scaled = Math.min(1, rms * 4);
        smoothed.current =
          scaled > smoothed.current
            ? scaled
            : smoothed.current * 0.88 + scaled * 0.12;
        setLevel(smoothed.current);
        frame = requestAnimationFrame(tick);
      };
      frame = requestAnimationFrame(tick);
    }

    void listen();

    return () => {
      stop = true;
      cancelAnimationFrame(frame);
      void ctx?.close();
      if (stream) for (const track of stream.getTracks()) track.stop();
    };
  }, [active]);

  return { level, spectrum };
}
