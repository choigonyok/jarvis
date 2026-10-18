#!/usr/bin/env python3
"""
tts: the operator's own voice, for the 대화 tab's 음성 mode.

Qwen3-TTS 1.7B Base (MLX) clones a voice from one short recording and its
transcript, and reads the agent's answers in it. It runs on the Mac mini's own
macOS, not in compose - MLX wants the Metal GPU, which a container cannot see -
and the gateway reaches it through the tunnel container at host.docker.internal.

Routes (Bearer JARVIS_API_TOKEN like every service; /health needs none):

  GET    /health   {"ok":true,"model":…,"loaded":bool,"voice":bool}
  GET    /voice    the recording on file: {"present":bool,"text":…,"seconds":…,"savedAt":…}
  PUT    /voice    {"text": transcript, "audio": base64 WAV}  → the voice from now on
  DELETE /voice    forget it (the stage falls back to the browser's own voice)
  POST   /speak    {"text":…, "lang":"korean"}  → audio/wav (24 kHz mono)

One generation at a time: the model is one, and two at once would only share
the GPU and finish later. The console speaks a reply sentence by sentence, so
a request is a sentence or two and the first sound arrives early.

State: ~/.jarvis-tts/voice.wav + voice.json. The model weights live in the
Hugging Face cache (~/.cache/huggingface); scripts/tts install fetches them.
"""

import argparse
import base64
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


def read_token(env_file: Path | None) -> str:
    token = os.environ.get("JARVIS_API_TOKEN", "")
    if token or env_file is None:
        return token
    try:
        for line in env_file.read_text().splitlines():
            if line.startswith("JARVIS_API_TOKEN="):
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

class Engine:
    """The model, the voice on file, and the one lock they share."""

    def __init__(self, model_id: str, state_dir: Path):
        self.model_id = model_id
        self.state_dir = state_dir
        self.voice_wav = state_dir / "voice.wav"
        self.voice_json = state_dir / "voice.json"
        self.lock = threading.Lock()
        self.model = None
        self.ref_audio = None  # mx.array, 24 kHz mono
        self.ref_text: str | None = None
        self.voice_meta: dict | None = None
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
        self.load_voice()
        if self.ref_audio is not None:
            self.warm_up()

    def warm_up(self) -> None:
        # The first request after a new voice also encodes the reference; done
        # here so the first real answer is not the one that waits for it.
        try:
            self.speak("안녕하세요.", "korean")
            log.info("목소리 준비 끝")
        except Exception as e:  # noqa: BLE001
            log.warning("예열에 실패했습니다: %s", e)

    # -- voice on file

    def load_voice(self) -> None:
        import mlx.core as mx

        if not (self.voice_wav.exists() and self.voice_json.exists()):
            self.ref_audio = self.ref_text = self.voice_meta = None
            return
        meta = json.loads(self.voice_json.read_text())
        pcm, rate = decode_wav(self.voice_wav.read_bytes())
        self.ref_audio = mx.array(resample(pcm, rate, SAMPLE_RATE))
        self.ref_text = meta["text"]
        self.voice_meta = meta

    def set_voice(self, text: str, wav: bytes) -> dict:
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
        with self.lock:
            self.state_dir.mkdir(parents=True, exist_ok=True)
            self.voice_wav.write_bytes(encode_wav(pcm, SAMPLE_RATE))
            self.voice_json.write_text(json.dumps(meta, ensure_ascii=False))
            if self.model is not None:
                # Forget the old voice's prompt tokens; the new one is encoded on first use.
                cache = getattr(self.model, "_icl_cache", None)
                if isinstance(cache, dict):
                    cache.clear()
            self.load_voice()
        threading.Thread(target=self.warm_up, daemon=True).start()
        return meta

    def clear_voice(self) -> None:
        with self.lock:
            for p in (self.voice_wav, self.voice_json):
                if p.exists():
                    p.unlink()
            self.ref_audio = self.ref_text = self.voice_meta = None
            if self.model is not None:
                cache = getattr(self.model, "_icl_cache", None)
                if isinstance(cache, dict):
                    cache.clear()

    # -- speaking

    def speak(self, text: str, lang: str) -> tuple[bytes, float, float]:
        """→ (wav bytes, audio seconds, wall seconds)."""
        import mlx.core as mx

        if self.model is None:
            raise RuntimeError("모델이 아직 준비되지 않았습니다.")
        if self.ref_audio is None or self.ref_text is None:
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
                    ref_audio=self.ref_audio,
                    ref_text=self.ref_text,
                    lang_code=lang,
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
    token: str

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

    def authorized(self) -> bool:
        if not self.token:
            return False
        auth = self.headers.get("Authorization", "")
        return auth == f"Bearer {self.token}"

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
            return self.reply_json(
                200,
                {
                    "ok": e.model is not None,
                    "model": e.model_id,
                    "loaded": e.model is not None,
                    "voice": e.ref_audio is not None,
                    **({"error": e.load_error} if e.load_error else {}),
                },
            )
        if not self.authorized():
            return self.error(401, "인증이 필요합니다.")
        if self.path == "/voice":
            meta = self.engine.voice_meta
            return self.reply_json(200, {"present": meta is not None, **(meta or {})})
        self.error(404, "그런 경로가 없습니다.")

    def do_PUT(self):  # noqa: N802
        if not self.authorized():
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
            meta = self.engine.set_voice(text, wav)
        except (ValueError, wave.Error, KeyError) as e:
            return self.error(400, str(e) or "녹음을 읽지 못했습니다.")
        log.info("새 목소리 %.1fs: %s", meta["seconds"], meta["text"][:40])
        self.reply_json(200, {"present": True, **meta})

    def do_DELETE(self):  # noqa: N802
        if not self.authorized():
            return self.error(401, "인증이 필요합니다.")
        if self.path != "/voice":
            return self.error(404, "그런 경로가 없습니다.")
        self.engine.clear_voice()
        self.reply_json(200, {"present": False})

    def do_POST(self):  # noqa: N802
        if not self.authorized():
            return self.error(401, "인증이 필요합니다.")
        if self.path != "/speak":
            return self.error(404, "그런 경로가 없습니다.")
        try:
            data = self.body_json()
            text = str(data.get("text", ""))
            lang = str(data.get("lang") or "korean").lower()
            wav, seconds, wall = self.engine.speak(text, lang)
        except LookupError as e:
            return self.error(409, str(e))
        except ValueError as e:
            return self.error(400, str(e))
        except RuntimeError as e:
            return self.error(503, str(e))
        except Exception as e:  # noqa: BLE001
            log.exception("생성 실패")
            return self.error(500, f"음성을 만들지 못했습니다: {e}")
        log.info("speak %d자 → %.1fs 음성, %.1fs 걸림 (RTF %.2f)", len(text), seconds, wall, wall / max(seconds, 0.01))
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
    ap.add_argument("--env-file", default=None, help="JARVIS_API_TOKEN 을 읽을 .env (환경변수가 없을 때)")
    args = ap.parse_args()

    logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(message)s", stream=sys.stdout)
    token = read_token(Path(args.env_file) if args.env_file else None)
    if not token:
        log.error("JARVIS_API_TOKEN 이 없습니다. 환경변수나 --env-file 로 주세요.")
        return 1

    engine = Engine(args.model, Path(args.state_dir))
    Handler.engine = engine
    Handler.token = token
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
