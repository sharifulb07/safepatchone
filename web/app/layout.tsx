import type { Metadata } from "next";
import "./globals.css";
import "./site.css";

export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "SafePatch · Evidence for every change",
  description: "Compare SafePatch editions and find the right foundation for software-change verification, shared proof, and organizational trust.",
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="en"><body>{children}</body></html>;
}
