import { Link } from "waku";
import { Mermaid } from "@/components/mermaid";

export default function Home() {
  return (
    <div className="flex-1">
      <section className="mx-auto flex max-w-5xl flex-col items-center px-6 py-8 text-center sm:py-10">
        <div className="rounded-full border px-3 py-1 text-sm text-fd-muted-foreground">
          Riot API rate limiting, queueing, and proxying
        </div>

        <h1 className="mt-4 text-3xl font-semibold tracking-tight sm:text-4xl">
          RiftRelay Docs
        </h1>

        <div className="mt-5 w-full max-w-4xl">
          <h2 className="text-center text-base font-semibold tracking-tight sm:text-lg">
            Where RiftRelay sits
          </h2>
          <p className="mx-auto mt-1.5 max-w-2xl text-center text-xs text-fd-muted-foreground sm:text-sm">
            Your apps send Riot API traffic through one place: it enforces
            Riot&apos;s rate limits, then queues and paces what&apos;s allowed,
            with optional{" "}
            <code className="rounded bg-fd-muted px-1 py-0.5 text-xs">
              X-Priority: high
            </code>{" "}
            for urgent calls.
          </p>
          <Mermaid
            className="!my-4 [&_svg]:max-h-[min(56vh,440px)] [&_svg]:w-auto [&_svg]:max-w-full"
            chart={`
%%{init: {'flowchart': {'nodeSpacing': 40, 'rankSpacing': 44, 'padding': 12}, 'themeVariables': {'fontSize': '15px'}}}%%
flowchart LR
    A[Client A] --> RR
    B[Client B] --> RR

    subgraph RR[RiftRelay]
        direction TB
        L[Enforce Riot rate limits]
        Q[Queue, pace & priority]
        L --> Q
    end

    RR --> D[Riot Games API]
`}
          />
        </div>

        <p className="mt-6 max-w-2xl text-base text-fd-muted-foreground sm:text-lg">
          RiftRelay is a rate-limiting proxy for the Riot Games API built in Go.
          It helps you smooth traffic, share your Riot API token, and expose
          Swagger UI, metrics, and profiling endpoints from one service.
        </p>

        <div className="mt-6 flex flex-col gap-3 sm:flex-row">
          <Link
            to="/docs"
            className="rounded-lg bg-fd-primary px-4 py-2 text-sm font-medium text-fd-primary-foreground"
          >
            Open Docs
          </Link>
          <a
            href="https://github.com/renja-g/RiftRelay"
            className="rounded-lg border px-4 py-2 text-sm font-medium"
          >
            GitHub
          </a>
        </div>

        <div className="mt-10 grid w-full max-w-4xl gap-4 text-left sm:grid-cols-2 lg:grid-cols-3">
          <Link
            to="/docs/quickstart"
            className="rounded-xl border p-5 transition-colors hover:bg-fd-muted/50"
          >
            <h2 className="text-sm font-semibold">Quickstart</h2>
            <p className="mt-2 text-sm text-fd-muted-foreground">
              Get RiftRelay running with Docker, Docker Compose, or directly
              from source.
            </p>
          </Link>

          <Link
            to="/docs/reference/configuration"
            className="rounded-xl border p-5 transition-colors hover:bg-fd-muted/50"
          >
            <h2 className="text-sm font-semibold">Configuration</h2>
            <p className="mt-2 text-sm text-fd-muted-foreground">
              Learn which environment variables are required, what the defaults
              are, and how timeouts and feature flags work.
            </p>
          </Link>

          <Link
            to="/docs/guides/usage"
            className="rounded-xl border p-5 transition-colors hover:bg-fd-muted/50"
          >
            <h2 className="text-sm font-semibold">Usage</h2>
            <p className="mt-2 text-sm text-fd-muted-foreground">
              Understand the proxy path format, priority requests, and how
              RiftRelay forwards traffic to Riot.
            </p>
          </Link>
        </div>
      </section>
    </div>
  );
}

export async function getConfig() {
  return {
    render: "static",
  };
}
