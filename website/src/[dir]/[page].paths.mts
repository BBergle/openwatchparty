// Pages for the repository documents with a two-segment route (`/docs/protocol`); see .vitepress/docs.mts.
// `owpRoutePaths` comes from the site config, so a running dev server picks up a changed docs list (see docs.mts).
export default { paths: () => globalThis.owpRoutePaths(2) }
