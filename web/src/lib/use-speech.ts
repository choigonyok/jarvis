"use client";

/**
 * Both halves of voice mode. Listening is the browser's SpeechRecognition.
 * Speaking is the operator's own voice when one is on file - Qwen3-TTS on the
 * Mac mini (tts/server.py), reached through /api/tts - and otherwise the
 * browser's speechSynthesis, which needs no server at all. Nothing goes to a
 * speech vendor either way, and there is no key to rotate.
 *
 * The cost is reach: recognition is Chromium and Safari only. Callers are
 * expected to check `supported` and fall back to typing.
 *
 * Each half is a module of its own - listening (speech-listen.ts) and
 * speaking (speech-speak.ts) - and this one is where the screens take them
 * from.
 */

export { capturesAlone, useRecognition, type Listening } from "@/lib/speech-listen";
export {
  playerReady,
  primePlayer,
  sentences,
  sharedContext,
  speakable,
  unlockSpeech,
  useSpeech,
  useVoiceStatus,
  type Speaker,
  type VoiceInfo,
  type VoiceState,
} from "@/lib/speech-speak";
