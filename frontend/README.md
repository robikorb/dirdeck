# Liquid Glass File Manager UI

React and TypeScript frontend embedded into the production Go binary.

## Local checks

```bash
npm ci
npm run lint
npm run build
```

The Vite development server is intended only for frontend development. The
supported deployment is the root Docker stack, which builds this UI and copies
`dist/` into the Go server image.

The Monaco editor is bundled locally. File content is fetched only through the
authenticated same-origin API and is not sent to a CDN.
