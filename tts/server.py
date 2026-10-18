#!/usr/bin/env python3
"""
tts: each person's own voice, for the 대화 tab's 음성 mode.

Qwen3-TTS 1.7B Base (MLX) clones a voice from one short recording and its
transcript, and reads the agent's answers in it. It runs on the Mac mini's own
macOS, not in compose - MLX wants the Metal GPU, which a container cannot see -
and the gateway reaches it through the tunnel container at host.docker.internal.

Two recordings, one each: the operator's and the guest's. Which one a request
reads, replaces, forgets or speaks in is decided by the token it carries, never
by anything it says - JARVIS_API_TOKEN is the operator's, JARVIS_GUEST_API_TOKEN
the guest's (the gateway hands the guest's requests on with it) - so the guest
cannot hear, change or speak in the operator's voice, nor the other way round.

Routes (Bearer token as above; /health needs none):

  GET    /health   {"ok":true,"model":…,"loaded":bool,"voice":bool,"voices":{"owner":bool,"guest":bool}}
  GET    /voice    the caller's recording: {"present":bool,"text":…,"seconds":…,"savedAt":…}
  PUT    /voice    {"text": transcript, "audio": base64 WAV}  → the caller's voice from now on
  DELETE /voice    forget it (the stage falls back to the browser's own voice)
  POST   /speak    {"text":…, "lang":"korean"}  → audio/wav (24 kHz mono), in the caller's voice

One generation at a time: the model is one, and two at once would only share
the GPU and finish later. The console speaks a reply sentence by sentence, so
a request is a sentence or two and the first sound arrives early.

State: ~/.jarvis-tts/voice.wav + voice.json (the operator's) and
~/.jarvis-tts/guest/ (the guest's). The model weights live in the Hugging Face
cache (~/.cache/huggingface); scripts/tts install fetches them.
"""

import argparse
import base64
import hmac
import io
import json
import logging
import os
import sys
import threading
import time
import wave
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import numpy as np

log = logging.getLogger("tts")

DEFAULT_MODEL = "mlx-community/Qwen3-TTS-12Hz-1.7B-Base-8bit"
SAMPLE_RATE = 24000
# A reference longer than this buys nothing and costs prefill on every
# request; shorter than the minimum and the clone has nothing to hold on to.
MIN_REF_SECONDS = 3.0
MAX_REF_SECONDS = 20.0
# The console splits a reply into sentences before asking; a single request
# this long is a bug on the caller's side, not a reading to attempt.
MAX_TEXT_CHARS = 600
# Keep MLX's buffer cache small: the model's 3 GB stays resident, the rest is
# returned after each request. This Mac mini runs the whole stack in 16 GB.
CACHE_LIMIT_BYTES = 256 * 1024 * 1024
# A ceiling MLX works under: past it the allocator frees cached buffers before
# asking for more. Measured on the 1.7B 8-bit: without it a short sentence
# peaks at 7.5 GB; at 3.8 GB it peaks near 5.3 GB and runs no slower.
MEMORY_LIMIT_BYTES = int(float(os.environ.get("TTS_MEMORY_LIMIT_GB", "3.8")) * 1e9)


def slot_tokens(env_file: Path | None) -> dict[str, str]:
    """Each token and whose recording it opens. The guest's only when it is set
    and differs from the operator's: one token for both would hand the guest
    the operator's voice."""
    owner = read_token(env_file, "JARVIS_API_TOKEN")
    guest = read_token(env_file, "JARVIS_GUEST_API_TOKEN")
    tokens = {owner: "owner"} if owner else {}
    if guest and guest != owner:
        tokens[guest] = "guest"
    return tokens


def read_token(env_file: Path | None, name: str = "JARVIS_API_TOKEN") -> str:
    token = os.environ.get(name, "")
    if token or env_file is None:
        return token
    try:
        for line in env_file.read_text().splitlines():
            if line.startswith(name + "="):
                return line.split("=", 1)[1].strip().strip('"').strip("'")
    except OSError as e:
        log.error("env 파일을 읽지 못했습니다: %s", e)
    return ""


# ---------------------------------------------------------------- audio

def decode_wav(data: bytes) -> tuple[np.ndarray, int]:
    """16/24/32-bit PCM or 32-bit float WAV → float32 mono in [-1, 1], its rate."""
    with wave.open(io.BytesIO(data)) as w:
        channels, width, rate = w.getnchannels(), w.getsampwidth(), w.getframerate()
        frames = w.readframes(w.getnframes())
    if width == 2:
        pcm = np.frombuffer(frames, dtype="<i2").astype(np.float32) / 32768.0
    elif width == 4:
        # The browser's own encoder writes 16-bit; 32-bit here is almost always float.
        pcm = np.frombuffer(frames, dtype="<f4").astype(np.float32)
        if np.abs(pcm).max(initial=0) > 1.5:
            pcm = np.frombuffer(frames, dtype="<i4").astype(np.float32) / 2147483648.0
    elif width == 3:
        raw = np.frombuffer(frames, dtype=np.uint8).reshape(-1, 3)
        ints = (raw[:, 0].astype(np.int32) | (raw[:, 1].astype(np.int32) << 8) | (raw[:, 2].astype(np.int32) << 16))
        ints = np.where(ints >= 1 << 23, ints - (1 << 24), ints)
        pcm = ints.astype(np.float32) / 8388608.0
    else:
        raise ValueError(f"지원하지 않는 WAV 비트 수: {width * 8}")
    if channels > 1:
        pcm = pcm.reshape(-1, channels).mean(axis=1)
    return pcm, rate


def resample(pcm: np.ndarray, rate: int, target: int) -> np.ndarray:
    if rate == target:
        return pcm
    from scipy.signal import resample_poly
    from math import gcd

    g = gcd(rate, target)
    return resample_poly(pcm, target // g, rate // g).astype(np.float32)


def trim_silence(pcm: np.ndarray, rate: int, threshold_db: float = -40.0, pad_ms: int = 150) -> np.ndarray:
    """Cut the quiet before the first word and after the last, leaving a short pad."""
    if pcm.size == 0:
        return pcm
    hop = rate // 100  # 10 ms
    frames = pcm[: pcm.size - pcm.size % hop].reshape(-1, hop)
    rms = np.sqrt((frames**2).mean(axis=1) + 1e-12)
    loud = np.where(20 * np.log10(rms) > threshold_db)[0]
    if loud.size == 0:
        raise ValueError("녹음에 목소리가 없습니다. 조용한 곳에서 다시 읽어 주세요.")
    pad = int(rate * pad_ms / 1000)
    start = max(0, loud[0] * hop - pad)
    end = min(pcm.size, (loud[-1] + 1) * hop + pad)
    return pcm[start:end]


def encode_wav(pcm: np.ndarray, rate: int) -> bytes:
    ints = np.clip(pcm, -1.0, 1.0)
    ints = (ints * 32767.0).astype("<i2")
    buf = io.BytesIO()
    with wave.open(buf, "wb") as w:
        w.setnchannels(1)
        w.setsampwidth(2)
        w.setframerate(rate)
        w.writeframes(ints.tobytes())
    return buf.getvalue()


# ---------------------------------------------------------------- engine

class Voice:
    """One person's recording on file, and what the model reads it as."""

    def __init__(self, folder: Path):
        self.folder = folder
        self.wav = folder / "voice.wav"
        self.json = folder / "voice.json"
        self.ref_audio = None  # mx.array, 24 kHz mono
        self.ref_text: str | None = None
        self.meta: dict | None = None


class Engine:
    """The model, each person's voice on file, and the one lock they share."""

    def __init__(self, model_id: str, state_dir: Path):
        self.model_id = model_id
        self.state_dir = state_dir
        # The operator's recording stays where it always was.
        self.voices = {"owner": Voice(state_dir), "guest": Voice(state_dir / "guest")}
        self.lock = threading.Lock()
        self.model = None
        self.load_error: str | None = None

    # -- model

    def load(self) -> None:
        import mlx.core as mx
        from mlx_audio.tts.utils import load_model

        mx.set_cache_limit(CACHE_LIMIT_BYTES)
        mx.set_memory_limit(MEMORY_LIMIT_BYTES)
        t0 = time.time()
        try:
            self.model = load_model(self.model_id)
        except Exception as e:  # noqa: BLE001 - a missing download must show up in /health, not kill the process
            self.load_error = str(e)
            log.error("모델을 올리지 못했습니다: %s", e)
            return
        mx.clear_cache()
        log.info("모델 준비 %s (%.0fs, 상주 %.1f GB)", self.model_id, time.time() - t0, mx.get_active_memory() / 1e9)
        for who in self.voices:
            self.load_voice(who)
            if self.voices[who].ref_audio is not None:
                self.warm_up(who)

    def warm_up(self, who: str) -> None:
        # The first request after a new voice also encodes the reference; done
        # here so the first real answer is not the one that waits for it.
        try:
            self.speak(who, "안녕하세요.", "korean")
            log.info("목소리 준비 끝 (%s)", who)
        except Exception as e:  # noqa: BLE001
            log.warning("예열에 실패했습니다 (%s): %s", who, e)

    # -- voice on file

    def load_voice(self, who: str) -> None:
        import mlx.core as mx

        v = self.voices[who]
        if not (v.wav.exists() and v.json.exists()):
            v.ref_audio = v.ref_text = v.meta = None
            return
        meta = json.loads(v.json.read_text())
        pcm, rate = decode_wav(v.wav.read_bytes())
        v.ref_audio = mx.array(resample(pcm, rate, SAMPLE_RATE))
        v.ref_text = meta["text"]
        v.meta = meta

    def forget_prompts(self) -> None:
        # The model keeps each voice's encoded prompt keyed by the recording
        # itself, so two voices never share one; a replaced or forgotten
        # recording's is dropped with the rest, and the other voice's is
        # encoded again on its next use.
        cache = getattr(self.model, "_icl_cache", None)
        if isinstance(cache, dict):
            cache.clear()

    def set_voice(self, who: str, text: str, wav: bytes) -> dict:
        text = " ".join(text.split())
        if not text:
            raise ValueError("대본이 비어 있습니다.")
        pcm, rate = decode_wav(wav)
        pcm = resample(pcm, rate, SAMPLE_RATE)
        pcm = trim_silence(pcm, SAMPLE_RATE)
        seconds = pcm.size / SAMPLE_RATE
        if seconds < MIN_REF_SECONDS:
            raise ValueError(f"녹음이 {seconds:.1f}초로 너무 짧습니다. {MIN_REF_SECONDS:.0f}초는 넘어야 합니다.")
        if seconds > MAX_REF_SECONDS:
            pcm = pcm[: int(MAX_REF_SECONDS * SAMPLE_RATE)]
            seconds = MAX_REF_SECONDS
        # Normalise to a sane level: a quiet phone recording clones as a whisper.
        peak = float(np.abs(pcm).max(initial=0))
        if peak > 0:
            pcm = pcm * min(4.0, 0.9 / peak)
        meta = {"text": text, "seconds": round(seconds, 1), "savedAt": time.strftime("%Y-%m-%dT%H:%M:%S%z")}
        v = self.voices[who]
        with self.lock:
            v.folder.mkdir(parents=True, exist_ok=True)
            v.wav.write_bytes(encode_wav(pcm, SAMPLE_RATE))
            v.json.write_text(json.dumps(meta, ensure_ascii=False))
            self.forget_prompts()
            self.load_voice(who)
        threading.Thread(target=self.warm_up, args=(who,), daemon=True).start()
        return meta

    def clear_voice(self, who: str) -> None:
        v = self.voices[who]
        with self.lock:
            for p in (v.wav, v.json):
                if p.exists():
                    p.unlink()
            v.ref_audio = v.ref_text = v.meta = None
            self.forget_prompts()

    # -- speaking

    def speak(self, who: str, text: str, lang: str) -> tuple[bytes, float, float]:
        """→ (wav bytes, audio seconds, wall seconds), in who's voice."""
        import mlx.core as mx

        if self.model is None:
            raise RuntimeError("모델이 아직 준비되지 않았습니다.")
        v = self.voices[who]
        if v.ref_audio is None or v.ref_text is None:
            raise LookupError("등록된 목소리가 없습니다.")
        text = " ".join(text.split())
        if not text:
            raise ValueError("읽을 글이 없습니다.")
        if len(text) > MAX_TEXT_CHARS:
            raise ValueError(f"한 번에 {MAX_TEXT_CHARS}자까지 읽습니다.")
        with self.lock:
            t0 = time.time()
            chunks = []
            try:
                for r in self.model.generate(
                    text=text,
                    ref_audio=v.ref_audio,
                    ref_text=v.ref_text,
                    lang_code=lang,
                    # Cooler sampling than the library's 0.9/50: a clone from
                    # one short recording drifts in timbre between sentences
                    # when sampled hot - heard as "a different voice every
                    # answer" (2026-10-19). Cooler keeps it on the recording.
                    temperature=0.6,
                    top_k=20,
                    verbose=False,
                ):
                    chunks.append(np.array(r.audio, dtype=np.float32).reshape(-1))
            finally:
                mx.clear_cache()
            wall = time.time() - t0
        if not chunks:
            raise RuntimeError("소리가 만들어지지 않았습니다.")
        pcm = np.concatenate(chunks)
        return encode_wav(pcm, SAMPLE_RATE), pcm.size / SAMPLE_RATE, wall


# ---------------------------------------------------------------- http

class Handler(BaseHTTPRequestHandler):
    server_version = "jarvis-tts/1"
    engine: Engine
    # Each token and whose recording it opens.
    tokens: dict[str, str]

    # Uniform small JSON answers; errors in the words the console shows.
    def reply_json(self, status: int, body: dict) -> None:
        data = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(data)))
        self.send_header("Cache-Control", "no-store")
        self.end_headers()
        self.wfile.write(data)

    def error(self, status: int, message: str) -> None:
        self.reply_json(status, {"error": message})

    def caller(self) -> str | None:
        """Whose recording this request is about, by its token; None for none."""
        auth = self.headers.get("Authorization", "")
        found = None
        for token, who in self.tokens.items():
            # Every token is compared, in constant time: neither which one
            # matched nor how much of a guess was right shows in the timing.
            if token and hmac.compare_digest(auth.encode(), f"Bearer {token}".encode()):
                found = who
        return found

    def body(self) -> bytes:
        length = int(self.headers.get("Content-Length") or 0)
        if length > 16 * 1024 * 1024:
            raise ValueError("요청이 너무 큽니다.")
        return self.rfile.read(length) if length else b""

    def body_json(self) -> dict:
        raw = self.body()
        try:
            data = json.loads(raw or b"{}")
        except json.JSONDecodeError as e:
            raise ValueError("JSON 을 읽지 못했습니다.") from e
        if not isinstance(data, dict):
            raise ValueError("JSON 객체가 필요합니다.")
        return data

    def log_message(self, fmt, *args):  # noqa: N802 - stdlib name
        log.info("%s " + fmt, self.address_string(), *args)

    # -- routes

    def do_GET(self):  # noqa: N802
        if self.path == "/health":
            e = self.engine
            present = {who: v.ref_audio is not None for who, v in e.voices.items()}
            return self.reply_json(
                200,
                {
                    "ok": e.model is not None,
                    "model": e.model_id,
                    "loaded": e.model is not None,
                    "voice": present["owner"],
                    "voices": present,
                    **({"error": e.load_error} if e.load_error else {}),
                },
            )
        who = self.caller()
        if who is None:
            return self.error(401, "인증이 필요합니다.")
        if self.path == "/voice":
            meta = self.engine.voices[who].meta
            return self.reply_json(200, {"present": meta is not None, **(meta or {})})
        self.error(404, "그런 경로가 없습니다.")

    def do_PUT(self):  # noqa: N802
        who = self.caller()
        if who is None:
            return self.error(401, "인증이 필요합니다.")
        if self.path != "/voice":
            return self.error(404, "그런 경로가 없습니다.")
        try:
            data = self.body_json()
            text = str(data.get("text", ""))
            audio = data.get("audio")
            if not isinstance(audio, str) or not audio:
                raise ValueError("녹음(audio)이 없습니다.")
            wav = base64.b64decode(audio, validate=True)
            meta = self.engine.set_voice(who, text, wav)
        except (ValueError, wave.Error, KeyError) as e:
            return self.error(400, str(e) or "녹음을 읽지 못했습니다.")
        log.info("새 목소리 (%s) %.1fs", who, meta["seconds"])
        self.reply_json(200, {"present": True, **meta})

    def do_DELETE(self):  # noqa: N802
        who = self.caller()
        if who is None:
            return self.error(401, "인증이 필요합니다.")
        if self.path != "/voice":
            return self.error(404, "그런 경로가 없습니다.")
        self.engine.clear_voice(who)
        self.reply_json(200, {"present": False})

    def do_POST(self):  # noqa: N802
        who = self.caller()
        if who is None:
            return self.error(401, "인증이 필요합니다.")
        if self.path != "/speak":
            return self.error(404, "그런 경로가 없습니다.")
        try:
            data = self.body_json()
            text = str(data.get("text", ""))
            lang = str(data.get("lang") or "korean").lower()
            wav, seconds, wall = self.engine.speak(who, text, lang)
        except LookupError as e:
            return self.error(409, str(e))
        except ValueError as e:
            return self.error(400, str(e))
        except RuntimeError as e:
            return self.error(503, str(e))
        except Exception as e:  # noqa: BLE001
            log.exception("생성 실패")
            return self.error(500, f"음성을 만들지 못했습니다: {e}")
        log.info("speak (%s) %d자 → %.1fs 음성, %.1fs 걸림 (RTF %.2f)", who, len(text), seconds, wall, wall / max(seconds, 0.01))
        self.send_response(200)
        self.send_header("Content-Type", "audio/wav")
        self.send_header("Content-Length", str(len(wav)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("X-TTS-Seconds", f"{seconds:.2f}")
        self.send_header("X-TTS-Wall", f"{wall:.2f}")
        self.end_headers()
        self.wfile.write(wav)


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--host", default="127.0.0.1")
    ap.add_argument("--port", type=int, default=8106)
    ap.add_argument("--model", default=os.environ.get("TTS_MODEL", DEFAULT_MODEL))
    ap.add_argument("--state-dir", default=os.environ.get("TTS_STATE_DIR", str(Path.home() / ".jarvis-tts")))
    ap.add_argument("--env-file", default=None, help="JARVIS_API_TOKEN·JARVIS_GUEST_API_TOKEN 을 읽을 .env (환경변수가 없을 때)")
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s", stream=sys.stdout)
    tokens = slot_tokens(Path(args.env_file) if args.env_file else None)
    if "owner" not in tokens.values():
        log.error("JARVIS_API_TOKEN 이 없습니다. 환경변수나 --env-file 로 주세요.")
        return 1
    if "guest" not in tokens.values():
        log.warning("JARVIS_GUEST_API_TOKEN 이 없거나 운영자 토큰과 같아 손님 목소리는 쓰지 않습니다.")

    engine = Engine(args.model, Path(args.state_dir))
    Handler.engine = engine
    Handler.tokens = tokens
    server = ThreadingHTTPServer((args.host, args.port), Handler)
    server.daemon_threads = True
    # Answer /health from the first second; the model loads behind it.
    threading.Thread(target=engine.load, daemon=True).start()
    log.info("듣는 중 http://%s:%d (모델 %s)", args.host, args.port, args.model)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    return 0


if __name__ == "__main__":
    sys.exit(main())
