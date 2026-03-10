# RiftRelay Docs

This directory contains the RiftRelay documentation site.

Content lives under `content/docs`.

## Development

```bash
pnpm install
pnpm dev
```

## Docker deployment

Build and serve the docs as a static site with nginx:

```bash
docker compose up -d
```

The docs will be available at http://localhost:3000. To use a different port, edit the `ports` mapping in `docker-compose.yml`.
