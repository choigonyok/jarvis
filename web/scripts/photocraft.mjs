#!/usr/bin/env node
/**
 * Puts PhotoCraft's web build (https://github.com/storytold/photocraft, MIT or
 * Apache-2.0) in public/photocraft/, where the 편집 tab frames it. It runs
 * before every build and dev server (package.json prebuild/predev) and does
 * nothing once the pinned version is in place.
 *
 * The site is static - the editor runs in the visitor's browser, nothing on
 * the Mac mini - so it ships with the console's own assets: Cloudflare serves
 * it behind the same Access login, and the app's 11 MB of WebAssembly comes
 * pre-split into parts under the 25 MiB file limit (HOSTING.md in the zip).
 * It is fetched rather than committed: the repository is public, and every
 * PhotoCraft release would add another 12 MB to its history.
 *
 * Pinned by version and SHA-256, both here: a release replaced upstream, or a
 * download changed on the way, stops the build instead of reaching the
 * console. To upgrade, change the version and the hash from the release's
 * SHA256SUMS.txt.
 *
 * Two things are added to the release:
 * - Pretendard, served as the Hangul fallback (fonts/manifest.txt, the app's
 *   own format). The app bundles Inter only, so Korean typed on a picture
 *   would draw as boxes. Pretendard's Latin is Inter's, so mixed text matches.
 * - A Content-Security-Policy in index.html. The editor runs on the console's
 *   origin; the policy keeps its code from sending anything to another host.
 */
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, renameSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { crc32, inflateRawSync } from "node:zlib";

const PHOTOCRAFT = {
  version: "0.12.0",
  url: "https://github.com/storytold/photocraft/releases/download/v0.12.0/photocraft-web-0.12.0.zip",
  sha256: "c2451e19bfd448de780d01a2dfc233924223a0020370af432c58c0c5ee9ec21e",
};

const PRETENDARD = {
  version: "1.3.9",
  files: [
    {
      to: "fonts/pretendard/Pretendard-Regular.ttf",
      url: "https://cdn.jsdelivr.net/npm/pretendard@1.3.9/dist/public/static/alternative/Pretendard-Regular.ttf",
      sha256: "6d0af5258997aec7354a6e340fc2325ba321c410ca48b3af858c8c3d6e92a324",
    },
    {
      // The SIL Open Font License asks for the licence to travel with the font.
      to: "fonts/pretendard/LICENSE.txt",
      url: "https://cdn.jsdelivr.net/npm/pretendard@1.3.9/dist/LICENSE.txt",
      sha256: "b04538c9abec39a3db75108cf0af0fd9c77032fe8aa2cf38345b4d250e98e38e",
    },
  ],
};

/** Bumped when what this script writes changes, so an older install is replaced. */
const LAYOUT = 1;
const STAMP = `photocraft ${PHOTOCRAFT.version}, pretendard ${PRETENDARD.version}, layout ${LAYOUT}\n`;

/** Release files the site does not need: docs, and header samples for other hosts. */
const SKIP = new Set(["README.md", "HOSTING.md", "_headers", ".htaccess"]);

const web = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const target = path.join(web, "public", "photocraft");
const stampFile = path.join(target, "version.txt");
// Built next to the site's folder but outside public/, so a run that dies
// half way leaves nothing that gets served.
const staging = path.join(web, "node_modules", ".cache", "photocraft-staging");

/** Bytes that are not the pinned ones: never shipped, whatever is installed already. */
class Mismatch extends Error {}

const sha256 = (bytes) => createHash("sha256").update(bytes).digest("hex");

async function download(url, expected) {
  const res = await fetch(url, { redirect: "follow" });
  if (!res.ok) throw new Error(`${url}: HTTP ${res.status}`);
  const bytes = Buffer.from(await res.arrayBuffer());
  const got = sha256(bytes);
  if (got !== expected) throw new Mismatch(`${url}: SHA-256 ${got}, expected ${expected}`);
  return bytes;
}

/**
 * The entries of a zip: name and contents. Enough for a release zip - stored
 * or deflated entries, no ZIP64 - and the archive's hash is checked before
 * this reads it.
 */
function unzip(zip) {
  let eocd = -1;
  for (let i = zip.length - 22; i >= Math.max(0, zip.length - 22 - 0xffff); i--) {
    if (zip.readUInt32LE(i) === 0x06054b50) {
      eocd = i;
      break;
    }
  }
  if (eocd < 0) throw new Error("not a zip file");
  const count = zip.readUInt16LE(eocd + 10);
  let at = zip.readUInt32LE(eocd + 16);
  const entries = [];
  for (let n = 0; n < count; n++) {
    if (zip.readUInt32LE(at) !== 0x02014b50) throw new Error("broken central directory");
    const method = zip.readUInt16LE(at + 10);
    const crc = zip.readUInt32LE(at + 16);
    const size = zip.readUInt32LE(at + 20);
    const nameLength = zip.readUInt16LE(at + 28);
    const extraLength = zip.readUInt16LE(at + 30);
    const commentLength = zip.readUInt16LE(at + 32);
    const local = zip.readUInt32LE(at + 42);
    const name = zip.toString("utf8", at + 46, at + 46 + nameLength);
    at += 46 + nameLength + extraLength + commentLength;
    if (name.endsWith("/")) continue;

    const start = local + 30 + zip.readUInt16LE(local + 26) + zip.readUInt16LE(local + 28);
    const raw = zip.subarray(start, start + size);
    const data = method === 0 ? raw : method === 8 ? inflateRawSync(raw) : null;
    if (!data) throw new Error(`${name}: compression method ${method}`);
    if (crc32(data) !== crc) throw new Error(`${name}: CRC mismatch`);
    entries.push({ name, data });
  }
  return entries;
}

/**
 * index.html with a Content-Security-Policy ahead of everything else in it.
 * Its one inline script (the boot) is allowed by hash; WebAssembly needs
 * 'wasm-unsafe-eval'; the app fetches its parts and fonts from here.
 */
function withPolicy(html) {
  const inline = [...html.matchAll(/<script(?![^>]*\bsrc=)[^>]*>([\s\S]*?)<\/script>/g)].map(
    (m) => `'sha256-${createHash("sha256").update(m[1]).digest("base64")}'`,
  );
  const policy = [
    "default-src 'self'",
    `script-src 'self' 'wasm-unsafe-eval' ${inline.join(" ")}`,
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data: blob:",
    "font-src 'self' data:",
    "connect-src 'self' data: blob:",
    "worker-src 'self' blob:",
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'none'",
  ].join("; ");
  const meta = `<meta http-equiv="Content-Security-Policy" content="${policy}">`;
  const charset = /<meta charset="utf-8">/i;
  if (!charset.test(html)) throw new Error("index.html: no <meta charset> to put the policy after");
  return html.replace(charset, (m) => `${m}\n  ${meta}`);
}

async function install() {
  const zip = await download(PHOTOCRAFT.url, PHOTOCRAFT.sha256);
  const prefix = `photocraft-web-${PHOTOCRAFT.version}/`;
  rmSync(staging, { recursive: true, force: true });

  const write = (name, data) => {
    const to = path.resolve(staging, name);
    if (!to.startsWith(staging + path.sep)) throw new Error(`${name}: outside the site`);
    mkdirSync(path.dirname(to), { recursive: true });
    writeFileSync(to, data);
  };

  try {
    for (const { name, data } of unzip(zip)) {
      if (!name.startsWith(prefix)) throw new Error(`${name}: not in ${prefix}`);
      const rel = name.slice(prefix.length);
      if (SKIP.has(rel)) continue;
      write(rel, rel === "index.html" ? withPolicy(data.toString("utf8")) : data);
    }
    if (!existsSync(path.join(staging, "index.html"))) throw new Error("the release has no index.html");

    for (const file of PRETENDARD.files) write(file.to, await download(file.url, file.sha256));
    write(
      "fonts/manifest.txt",
      [
        "# family | style | file | scripts | licence | licence file | sha256 | source",
        // Kore makes it the fallback for Hangul; the app fetches it up front
        // for a browser whose languages include Korean.
        `Pretendard | Regular | ${PRETENDARD.files[0].to} | Kore | OFL-1.1 | ${PRETENDARD.files[1].to} | ${PRETENDARD.files[0].sha256} | https://github.com/orioncactus/pretendard`,
        "",
      ].join("\n"),
    );
    write("version.txt", STAMP);

    rmSync(target, { recursive: true, force: true });
    renameSync(staging, target);
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}

const installed = existsSync(stampFile) ? readFileSync(stampFile, "utf8") : null;
if (installed !== STAMP) {
  try {
    await install();
    console.log(`photocraft: ${STAMP.trim()} -> public/photocraft`);
  } catch (e) {
    if (!installed || e instanceof Mismatch) {
      console.error(`photocraft: could not install (${e.message}). The 편집 tab needs public/photocraft.`);
      process.exit(1);
    }
    // An older install still works: a build is not stopped by an unreachable GitHub.
    console.warn(`photocraft: kept ${installed.trim()} (${e.message})`);
  }
}
