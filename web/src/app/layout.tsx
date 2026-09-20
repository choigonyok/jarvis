import type { Metadata } from "next";
import { Instrument_Sans, JetBrains_Mono } from "next/font/google";
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
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html
      lang="ko"
      className={`dark ${instrument.variable} ${jetbrains.variable} h-full antialiased`}
    >
      <body className="h-full overflow-hidden">{children}</body>
    </html>
  );
}
