import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig, type DefaultTheme } from 'vitepress'
import {
  githubSlug,
  label,
  languageTabs,
  ownerInputs,
  published,
  repoLinks,
  repoRoot,
  repoUrl,
  routePaths,
  sectionLinks,
  siteUrl,
  type Group,
} from './docs.mts'

const live = published()
// For the paths files of the dynamic routes; see `owpRoutePaths` in docs.mts.
globalThis.owpRoutePaths = routePaths

function items(group: Group): DefaultTheme.SidebarItem[] {
  return live.filter((doc) => doc.group === group).map((doc) => ({ text: label(doc), link: `/${doc.route}` }))
}

const sidebar: DefaultTheme.SidebarItem[] = [
  { text: 'Integrate', items: [{ text: 'For app developers', link: '/developers' }, ...items('Integrate')] },
  { text: 'Specification', items: items('Specification') },
  { text: 'SDKs', items: items('SDKs') },
  { text: 'Relay', items: items('Relay') },
  { text: 'Policies', items: items('Policies') },
]

const developers = { text: 'Developers', link: '/developers' }
const protocol = { text: 'Protocol', link: '/docs/protocol' }
const selfHosting = { text: 'Self-hosting', link: '/relay#self-hosting', activeMatch: '^/relay' }
const sdks = items('SDKs')
const policies = live.filter((doc) => doc.group === 'Policies')

// The search box, the appearance switch and the mobile menus need JavaScript. Without it, every page stays readable,
// and the footer and the previous/next links lead to every other page. Code tabs need it too: without it, every
// language's sample shows, one after another.
const noScriptStyle =
  '<style>.VPNavBarSearch,.VPNavBarAppearance,.VPNavBarHamburger,.VPLocalNav .menu,' +
  '.vp-code-group .tabs{display:none!important}' +
  '.vp-code-group .blocks>*{display:block!important;margin-top:16px!important}</style>'

// The public relay list, served byte for byte at /relays.json: the SDKs refresh the list from there. The build copies
// it from the repository (scripts/check-links.mjs checks the copy) and the dev server serves the same file.
const relayList = path.join(repoRoot, 'relays.json')

export default defineConfig({
  srcDir: 'src',
  lang: 'en',
  title: 'Open Watch Party',
  description: 'Watch together across different apps. Each person plays their own copy; the room keeps everyone in sync.',
  cleanUrls: true,
  sitemap: { hostname: siteUrl },
  // VitePress leaves the body of 404.html empty and renders it in the browser; this is what readers without
  // JavaScript see there.
  transformHtml(html, _id, { page }) {
    if (page !== '404.md') return
    return html.replace(
      '<div id="app"></div>',
      '<div id="app"></div><noscript><main style="padding:96px 24px;text-align:center">' +
        '<h1 style="font-size:20px;font-weight:700">Page not found</h1><p>There is no page at this address. ' +
        '<a href="/" style="text-decoration:underline">Go to the home page</a>.</p></main></noscript>',
    )
  },
  buildEnd(site) {
    fs.copyFileSync(relayList, path.join(site.outDir, 'relays.json'))
  },
  head: [
    ['link', { rel: 'icon', href: '/favicon.svg', type: 'image/svg+xml' }],
    ['link', { rel: 'icon', href: '/favicon.ico', sizes: '32x32' }],
    ['link', { rel: 'apple-touch-icon', href: '/apple-touch-icon.png' }],
    ['noscript', {}, noScriptStyle],
  ],
  markdown: {
    anchor: { slugify: githubSlug },
    // The theme's default light code colours fall below 4.5:1 on the code background.
    theme: { light: 'github-light-high-contrast', dark: 'github-dark-default' },
    // `{.class}` attribute syntax isn't GitHub markdown; the rendered documents are.
    attrs: { disable: true },
    externalLinks: { target: '_self', rel: 'noreferrer' },
    config(md) {
      md.use(repoLinks)
      md.use(sectionLinks)
      md.use(ownerInputs)
      md.use(languageTabs)
    },
  },
  vite: {
    resolve: {
      // theme/HomeMain.vue: the home layout with a main landmark (VitePress "Overriding Internal Components").
      alias: [{ find: /^.*\/VPHome\.vue$/, replacement: fileURLToPath(new URL('./theme/HomeMain.vue', import.meta.url)) }],
    },
    plugins: [
      {
        name: 'owp-relay-list',
        configureServer(server) {
          server.middlewares.use('/relays.json', (_req, res) => {
            res.setHeader('Content-Type', 'application/json')
            res.end(fs.readFileSync(relayList))
          })
        },
      },
    ],
  },
  themeConfig: {
    nav: [
      developers,
      protocol,
      { text: 'SDKs', items: sdks, activeMatch: '^/sdk/' },
      selfHosting,
    ],
    sidebar,
    outline: { level: [2, 3], label: 'On this page' },
    socialLinks: [{ icon: 'github', link: repoUrl, ariaLabel: 'Open Watch Party on GitHub' }],
    editLink: {
      // VitePress serializes this function into the client bundle, so it can't use variables from this file.
      pattern: ({ frontmatter, filePath }) =>
        `https://github.com/edde746/openwatchparty/blob/main/${frontmatter.source ?? `website/src/${filePath}`}`,
      text: 'Source on GitHub',
    },
    search: { provider: 'local' },
    footer: {
      message: [
        ...[developers, protocol, selfHosting].map((item) => `<a href="${item.link}">${item.text}</a>`),
        ...policies.map((doc) => `<a href="/${doc.route}">${label(doc)}</a>`),
        `<a href="${repoUrl}">GitHub</a>`,
      ].join(' · '),
      copyright: 'Open Watch Party is open source under the Apache License 2.0. This site sets no cookies and runs no analytics.',
    },
  },
})
