import { pathProxy } from "@/lib/proxy";

// Each person's cloned voice (tts/server.py on the Mac mini), through the
// gateway. Three doors: the recording on file (/voice: read, replace, forget)
// and a sentence to read (/speak). The operator and the guest each have their
// own: the guest's requests go on with the guest's token (the gateway hands
// them to the service with the guest's own), and the service keeps one
// recording per token - the guest never hears, changes or speaks in the
// operator's voice.
const TTS_URL = process.env.TTS_URL ?? "http://localhost:8106";
const allowed = new Set(["voice", "speak"]);

export const runtime = "nodejs";
export const dynamic = "force-dynamic";

const proxy = pathProxy(TTS_URL, {
  label: "목소리 서비스",
  allow: (p) => allowed.has(p),
  // How long the clip is, so the stage can light the caption in step with it.
  responseHeaders: ["content-length", "x-tts-seconds"],
});

export const GET = proxy;
export const POST = proxy;
export const PUT = proxy;
export const DELETE = proxy;
