# Console frontend

`App.tsx` renders eight pages with service data from the Go `Bridge` through `api.ts`.
`model.ts` describes the safe snapshot. Service disconnection preserves explicitly stale
readings and disables maintenance. Only the theme is stored locally.
