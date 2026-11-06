import { PhotoCraft } from "@/components/edit/photocraft";
import { PushBridge } from "@/components/shell/push-toast";
import { Toasts } from "@/components/shell/toasts";
import type { Metadata, Viewport } from "next";
import { Barlow_Condensed, Instrument_Sans, JetBrains_Mono } from "next/font/google";
import { cookies, headers } from "next/headers";
import { RoleProvider } from "@/components/shell/role";
import { NoZoom } from "@/components/shell/no-zoom";
import { PullToRefresh } from "@/components/shell/pull-to-refresh";
import { ViewportProbe } from "@/components/shell/viewport-probe";
import { ROLE_HEADER } from "@/lib/role";
import { BAR_COLOR, THEME_COOKIE, themeOf } from "@/lib/theme-name";
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
  // right shape for something you open to approve one thing and close. The
  // status bar is its own, opaque, and the page starts below it: iOS paints
  // it the page's colour with the clock in ink by day and white by night, and
  // follows the header's theme switch as it happens (measured on an iPhone 17
  // Pro, iOS 26.2). black-translucent ran the page under the clock, and iOS
  // 26.1 draws a dark blur over the top of such an app that no page can take
  // off - with the dark strip the light theme laid under the clock, the notch
  // looked wrong (2026-11-07). iOS reads this when the app is added to the
  // home screen: an app added before keeps the old bar until it is added
  // again.
  appleWebApp: { capable: true, statusBarStyle: "default", title: "Jarvis" },
  // Next emits only the newer "mobile-web-app-capable"; iOS still keys a
  // home-screen app's status bar off the Apple name.
  other: { "apple-mobile-web-app-capable": "yes" },
  icons: {
    // A tab is 16px: the orb alone on a clear ground, the full icon above that.
    icon: [
      { url: "/icons/favicon-32.v3.png", sizes: "32x32", type: "image/png" },
      { url: "/icons/icon-192.v3.png", sizes: "192x192", type: "image/png" },
    ],
    apple: [{ url: "/icons/apple-touch-icon.v3.png", sizes: "180x180", type: "image/png" }],
  },
};

export async function generateViewport(): Promise<Viewport> {
  return {
    width: "device-width",
    initialScale: 1,
    // cover lets the background run under the notch and the home indicator;
    // the safe-area insets are what keep controls out from under them.
    viewportFit: "cover",
    // The browser's own bars: the page's background, in the theme the
    // header's switch chose (lib/theme-name.ts). The switch repaints them.
    themeColor: BAR_COLOR[themeOf((await cookies()).get(THEME_COOKIE)?.value)],
    // maximum-scale=1 is what stops iOS from zooming in when a text field takes
    // focus. Safari ignores it for a pinch since iOS 10, so the browser can
    // still be pinch-zoomed; the home-screen app locks that too
    // (components/shell/no-zoom.tsx).
    maximumScale: 1,
  };
}

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // Stamped by the middleware; absent on /login, where nobody is signed in yet.
  const role = (await headers()).get(ROLE_HEADER) === "guest" ? "guest" : "owner";
  // Sent already in the chosen theme (globals.css reads data-theme): no flash
  // of the other one before the page's script runs.
  const theme = themeOf((await cookies()).get(THEME_COOKIE)?.value);
  return (
    <html
      lang="ko"
      data-theme={theme}
      className={`${instrument.variable} ${jetbrains.variable} ${scoreboard.variable} antialiased`}
    >
      <body className="overflow-hidden">
        <RoleProvider role={role}>
          {children}
          {/* The 편집 tab's editor: here rather than in the tab, so it outlives a visit to another one. */}
          <PhotoCraft />
          <PushBridge />
          <Toasts />
        </RoleProvider>
        <ViewportProbe />
        <NoZoom />
        <PullToRefresh />
      </body>
    </html>
  );
}
