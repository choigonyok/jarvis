import { Screen } from "@/components/screen/screen";

// Read per request, not at build time: the right address depends on where the
// page is being viewed from, and that is not knowable when the image is built.
export const dynamic = "force-dynamic";

export default function Page() {
  return <Screen vncUrl={process.env.JARVIS_VNC_URL ?? ""} />;
}
