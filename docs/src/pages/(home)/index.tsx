import { Link } from "waku";
import { RequestFlowDiagram } from "@/components/request-flow-diagram";

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

        <p className="mt-6 max-w-2xl text-base text-fd-muted-foreground sm:text-lg">
          A Go proxy that sits in front of the Riot Games API and handles rate
          limiting so your app doesn't have to.
        </p>

        <div className="mt-5 w-full max-w-2xl">
          <RequestFlowDiagram className="!my-4" />
        </div>

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
              Docker, Docker Compose, or from source.
            </p>
          </Link>

          <Link
            to="/docs/reference/configuration"
            className="rounded-xl border p-5 transition-colors hover:bg-fd-muted/50"
          >
            <h2 className="text-sm font-semibold">Configuration</h2>
            <p className="mt-2 text-sm text-fd-muted-foreground">
              Environment variables, defaults, and feature flags.
            </p>
          </Link>

          <Link
            to="/docs/guides/usage"
            className="rounded-xl border p-5 transition-colors hover:bg-fd-muted/50"
          >
            <h2 className="text-sm font-semibold">Usage</h2>
            <p className="mt-2 text-sm text-fd-muted-foreground">
              Path format, priority headers, and how requests get forwarded.
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
