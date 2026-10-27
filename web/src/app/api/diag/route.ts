// Temporary (2026-10-27): the chat screen's numbers from the phone, written
// to the Worker's log for `wrangler tail jarvis` (lib/diag.ts). Behind the
// same gate as everything else (middleware).
export const dynamic = "force-dynamic";

export async function POST(request: Request) {
  const body = (await request.text()).slice(0, 30_000);
  console.log("diag", body);
  return new Response(null, { status: 204 });
}
