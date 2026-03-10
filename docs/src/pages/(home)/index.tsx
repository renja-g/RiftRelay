import { Link } from "waku";

export default function Home() {
  return (
    <div className="flex-1">
      <section className="mx-auto flex max-w-5xl flex-col items-center justify-center px-6 py-24 text-center">
        <div className="rounded-full border px-3 py-1 text-sm text-fd-muted-foreground">
          Riot API rate limiting, queueing, and proxying
        </div>

        <h1 className="mt-6 text-4xl font-semibold tracking-tight sm:text-5xl">
          RiftRelay Docs
        </h1>

        <p className="mt-6 max-w-2xl text-base text-fd-muted-foreground sm:text-lg">
          RiftRelay is a rate-limiting proxy for the Riot Games API built in Go.
          It helps you smooth traffic, share your Riot API token, and expose
          Swagger UI, metrics, and profiling endpoints from one service.
        </p>

        <div className="mt-8 flex flex-col gap-3 sm:flex-row">
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

        <div className="mt-12 grid w-full max-w-4xl gap-4 text-left sm:grid-cols-2 lg:grid-cols-3">
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
