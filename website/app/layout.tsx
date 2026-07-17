import type { Metadata } from "next";
import { RootProvider } from "fumadocs-ui/provider/next";
import "./globals.css";

export const metadata: Metadata = {
  title: "tarjan — spin up a whole product's local dev environment",
  description:
    "tarjan brings up a product's entire local development environment from a single config: checks tools, clones repos, generates a VS Code workspace, and supervises every service. Terraform/Aspire, but for local dev.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="en" className="h-full antialiased" suppressHydrationWarning>
      <body className="min-h-full">
        <RootProvider
          theme={{ defaultTheme: "dark", enableSystem: false }}
        >
          {children}
        </RootProvider>
      </body>
    </html>
  );
}
