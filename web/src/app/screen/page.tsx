import { Screen } from "@/components/screen/screen";

// Read per request, not at build time: the right address depends on where the
// page is being viewed from, and that is not knowable when the image is built.
export const dynamic = "force-dynamic";

export default function Page() {
  // With a gateway configured the socket address comes with a fresh ticket
  // from /api/screen/ticket; without one, the page dials the host directly.
  return (
    <Screen
      vncUrl={process.env.JARVIS_VNC_URL ?? ""}
      ticketed={Boolean(process.env.JARVIS_VNC_GATEWAY)}
    />
  );
}
