import { Suspense } from "react";
import { redirect } from "next/navigation";
import { cookies, headers } from "next/headers";
import { accessIdentity } from "@/lib/access";
import { LoginForm } from "@/components/auth/login-form";
import { SESSION_COOKIE, valid } from "@/lib/auth";

export const dynamic = "force-dynamic";

export default async function Page() {
  // The middleware never runs here, so this page has to turn away a session
  // that is already good rather than offering a second login.
  const jar = await cookies();
  if (await valid(jar.get(SESSION_COOKIE)?.value)) redirect("/");
  // Signed in through Cloudflare Access already: there is nothing to ask.
  const assertion = (await headers()).get("cf-access-jwt-assertion") ?? jar.get("CF_Authorization")?.value;
  if ((await accessIdentity(assertion)) !== null) redirect("/");

  return (
    // useSearchParams needs a boundary for the prerender pass.
    <Suspense fallback={null}>
      <LoginForm />
    </Suspense>
  );
}
