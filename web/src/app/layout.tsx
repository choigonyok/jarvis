import type { Metadata, Viewport } from "next";
import { Instrument_Sans, JetBrains_Mono } from "next/font/google";
import { headers } from "next/headers";
import { RoleProvider } from "@/components/shell/role";
import { ROLE_HEADER } from "@/lib/role";
import "./globals.css";

const instrument = Instrument_Sans({
  variable: "--font-instrument",
  subsets: ["latin"],
  weight: ["400", "500", "600"],
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
    icon: [{ url: "/icons/icon-192.png", sizes: "192x192", type: "image/png" }],
    apple: [{ url: "/icons/apple-touch-icon.png", sizes: "180x180", type: "image/png" }],
  },
};

export const viewport: Viewport = {
  width: "device-width",
  initialScale: 1,
  // cover lets the background run under the notch and the home indicator;
  // the safe-area insets are what keep controls out from under them.
  viewportFit: "cover",
  themeColor: "#0b0d12",
  // Pinch-zoom stays available - capping it would fail anyone who needs to
  // magnify a proposal before approving it.
  maximumScale: 5,
};

export default async function RootLayout({ children }: LayoutProps<"/">) {
  // Stamped by the middleware; absent on /login, where nobody is signed in yet.
  const role = (await headers()).get(ROLE_HEADER) === "guest" ? "guest" : "owner";
  return (
    <html
      lang="ko"
      className={`dark ${instrument.variable} ${jetbrains.variable} antialiased`}
    >
      <head>
        {/* iOS home-screen apps with a translucent status bar report a
            viewport shorter than the screen - by the status bar's height -
            while drawing from the very top, so the page stops short and the
            bottom strip stays empty. Measured rather than assumed: the
            difference, only when launched from the home screen, only in
            portrait, becomes --standalone-gap, and the root and the bottom
            bar stretch by it. Zero everywhere else. */}
        <script
          dangerouslySetInnerHTML={{
            __html: `(function(){function f(){var s=window.navigator.standalone||matchMedia("(display-mode: standalone)").matches;var p=matchMedia("(orientation: portrait)").matches;var g=0;if(s&&p){g=Math.round(screen.height-window.innerHeight);if(g<0||g>120)g=0;}document.documentElement.style.setProperty("--standalone-gap",g+"px");}f();addEventListener("resize",f);addEventListener("orientationchange",f);})();`,
          }}
        />
      </head>
      <body className="overflow-hidden">
        <RoleProvider role={role}>{children}</RoleProvider>
      </body>
    </html>
  );
}
