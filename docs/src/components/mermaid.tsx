"use client";

import { useTheme } from "next-themes";
import { useEffect, useId, useRef } from "react";
import { cn } from "@/lib/cn";

function resolveIsDark(resolvedTheme: string | undefined): boolean {
  if (resolvedTheme === "dark") return true;
  if (resolvedTheme === "light") return false;
  if (typeof document === "undefined") return false;
  return document.documentElement.classList.contains("dark");
}

export function Mermaid({
  chart,
  className,
}: {
  chart: string;
  className?: string;
}) {
  const id = useId();
  const containerRef = useRef<HTMLDivElement>(null);
  const { resolvedTheme } = useTheme();

  useEffect(() => {
    let cancelled = false;
    const isDark = resolveIsDark(resolvedTheme);

    void (async () => {
      const mermaid = (await import("mermaid")).default;
      mermaid.initialize({
        startOnLoad: false,
        theme: isDark ? "dark" : "default",
        securityLevel: "strict",
        fontFamily: "inherit",
      });

      const cleanId = `mermaid-${id.replaceAll(/[^a-zA-Z0-9_-]/g, "")}`;

      const { svg } = await mermaid.render(cleanId, chart);
      if (!cancelled && containerRef.current) {
        containerRef.current.innerHTML = svg;
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [chart, id, resolvedTheme]);

  return (
    <div
      ref={containerRef}
      className={cn(
        "my-6 flex justify-center overflow-x-auto [&_svg]:max-w-full",
        className,
      )}
      role="img"
      aria-label="Request flow diagram"
    />
  );
}
