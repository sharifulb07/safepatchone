import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "Pricing · SafePatch",
  description: "Compare SafePatch editions and find the right foundation for software-change verification, shared proof, and organizational trust.",
};
export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="en"><body>{children}</body></html>;
}
