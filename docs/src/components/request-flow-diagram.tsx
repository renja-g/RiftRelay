import requestFlowDark from "@/assets/diagrams/request-flow-dark.svg?url";
import requestFlowLight from "@/assets/diagrams/request-flow-light.svg?url";
import { cn } from "@/lib/cn";

export function RequestFlowDiagram({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "my-6 w-full [&_img]:h-auto [&_img]:w-full [&_img]:max-w-full",
        className,
      )}
      role="img"
      aria-label="Request flow diagram: clients connect to RiftRelay, which enforces rate limits and queues traffic to the Riot Games API"
    >
      <img
        src={requestFlowLight}
        alt=""
        className="dark:hidden"
        decoding="async"
        fetchPriority="high"
      />
      <img
        src={requestFlowDark}
        alt=""
        className="hidden dark:block"
        decoding="async"
        fetchPriority="high"
      />
    </div>
  );
}
