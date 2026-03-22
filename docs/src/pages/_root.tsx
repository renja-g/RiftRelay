import type { ReactNode } from "react";
import { Provider } from "@/components/provider";
import "@/styles/globals.css";

export default async function RootElement({
  children,
}: {
  children: ReactNode;
}) {
  return (
    <html lang="en" suppressHydrationWarning>
      <head>
        <title>RiftRelay</title>
        <link rel="icon" href="/favicon.png" type="image/png" sizes="460x460" />
      </head>
      <body data-version="1.0" className="flex flex-col min-h-screen">
        <Provider>{children}</Provider>
      </body>
    </html>
  );
}

export async function getConfig() {
  return {
    render: "static",
  } as const;
}
