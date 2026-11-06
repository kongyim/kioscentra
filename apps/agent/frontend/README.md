# Local desktop console — Phase 4

React / TypeScript / Vite / MUI, embedded by Wails v3 with the `desktop` Go build tag.
Use Node 24.11+; run `npm ci --ignore-scripts`, `npm run build`, and `npm test` here.
The Go desktop build requires the generated `dist` directory. The normal Go build is headless.

`npm run dev` previews layout only; actual data requires Wails and an authenticated local
service. No demo telemetry or browser-side privileged APIs are provided.

See [Phase 4 setup](../../../docs/development/phase4.md).
