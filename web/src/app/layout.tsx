import { PushBridge } from "@/components/shell/push-toast";
import type { Metadata, Viewport } from "next";
import { Barlow_Condensed, Instrument_Sans, JetBrains_Mono } from "next/font/google";
import { headers } from "next/headers";
import { RoleProvider } from "@/components/shell/role";
import { NoZoom } from "@/components/shell/no-zoom";
import { PullToRefresh } from "@/components/shell/pull-to-refresh";
import { ViewportProbe } from "@/components/shell/viewport-probe";
import { ROLE_HEADER } from "@/lib/role";
import "./globals.css";

const instrument = Instrument_Sans({
  variable: "--font-instrument",
  subsets: ["latin"],
  weight: ["400", "500", "600"],
});

// The workout tab's numerals only - loads, reps, the clock. A condensed
// athletic face reads like the whiteboard at the gym, and fits a five-digit
// load in a stepper without shrinking it.
const scoreboard = Barlow_Condensed({
  variable: "--font-scoreboard",
  subsets: ["latin"],
  weight: ["500", "600"],
});

const jetbrains = JetBrains_Mono({
  variable: "--font-jetbrains",
  subsets: ["latin"],
  weight: ["400", "500"],
});

export const metadata: Metadata = {
  title: "Jarvis",
  description: "개인 AI 어시스턴트와의 대화, 그리고 실행 요청 결재.",
  // Added to the home screen this runs without Safari's chrome, which is the
  // right shape for something you open to approve one thing and close.
  appleWebApp: { capable: true, statusBarStyle: "black-translucent", title: "Jarvis" },
  // Next emits only the newer "mobile-web-app-capable"; iOS still keys the
  // translucent status bar off the Apple name, and without it a home-screen
  // launch draws the page below the notch and leaves the strip blank.
  other: { "apple-mobile-web-app-capable": "yes" },
  icons: {
    // A tab is 16px: the simplified three-line mark, the full one above that.
    icon: [
      { url: "/icons/favicon-32.v2.png", sizes: "32x32", type: "image/png" },
      { url: "/icons/icon-192.v2.png", sizes: "192x192", type: "image/png" },
    ],
    apple: [{ url: "/icons/apple-touch-icon.v2.png", sizes: "180x180", type: "image/png" }],
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  // cover lets the background run under the notch and the home indicator;
  // the safe-area insets are what keep controls out from under them.
  viewportFit: "cover",
  themeColor: "#0b0d12",
  // maximum-scale=1 is what stops iOS from zooming in when a text field takes
  // focus. Safari ignores it for a pinch since iOS 10, so the browser can
  // still be pinch-zoomed; the home-screen app locks that too
  // (components/shell/no-zoom.tsx).
  maximumScale: 1,
};

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // Stamped by the middleware; absent on /login, where nobody is signed in yet.
  const role = (await headers()).get(ROLE_HEADER) === "guest" ? "guest" : "owner";
  return (
    <html
      lang="ko"
      className={`dark ${instrument.variable} ${jetbrains.variable} ${scoreboard.variable} antialiased`}
    >
      <body className="overflow-hidden">
        <RoleProvider role={role}>
          {children}
          <PushBridge />
        </RoleProvider>
        <ViewportProbe />
        <NoZoom />
        <PullToRefresh />
      </body>
    </html>
  );
}
