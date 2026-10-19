// Which build is serving. An open page asks once when it comes up and again on
// a pull to refresh: a different answer means a newer build has been deployed
// since, and only a full load brings its code (components/shell/pull-to-refresh.tsx).
export const dynamic = "force-dynamic";

export function GET() {
  return Response.json(
    { build: process.env.JARVIS_BUILD ?? "" },
    { headers: { "cache-control": "no-store" } },
  );
}
