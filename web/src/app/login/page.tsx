import { Suspense } from "react";
import { redirect } from "next/navigation";
import { cookies } from "next/headers";
import { LoginForm } from "@/components/auth/login-form";
import { SESSION_COOKIE, valid } from "@/lib/auth";

export const dynamic = "force-dynamic";

export default async function Page() {
  // The middleware never runs here, so this page has to turn away a session
  // that is already good rather than offering a second login.
  const jar = await cookies();
  if (await valid(jar.get(SESSION_COOKIE)?.value)) redirect("/");

  return (
    // useSearchParams needs a boundary for the prerender pass.
    <Suspense fallback={null}>
      <LoginForm />
    </Suspense>
  );
}
