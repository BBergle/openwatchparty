// Checks the built site in .vitepress/dist (run `npm run build` first) and exits 1, listing every problem, when:
// - a link or resource on the site points to a file that wasn't built, or a #fragment to an ID the page doesn't have;
// - a link to this repository on GitHub (…/blob/main/<path> or …/tree/main/<path>) names a path that doesn't exist;
// - a page loads anything (script, style, font, image, frame) from another site;
// - sitemap.xml lists a page that wasn't built;
// - relays.json is missing or differs by any byte from the repository's relays.json (the SDKs refresh the public relay
//   list from https://openwatch.party/relays.json).

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const websiteDir = fileURLToPath(new URL('..', import.meta.url))
const dist = path.join(websiteDir, '.vitepress/dist')
const repoRoot = path.join(websiteDir, '..')
const siteOrigin = 'https://openwatch.party'
const repoFileRE = /^https:\/\/github\.com\/edde746\/openwatchparty\/(blob|tree)\/main\/([^?#]+)/

function walk(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name)
    return entry.isDirectory() ? walk(full) : [full]
  })
}

function decodeEntities(value) {
  return value
    .replace(/&quot;/g, '"')
    .replace(/&#39;/g, "'")
    .replace(/&lt;/g, '<')
    .replace(/&gt;/g, '>')
    .replace(/&amp;/g, '&')
}

/**
 * The built file a site path is served from, or undefined. Clean URLs: `/a` serves `a` or `a.html`, or redirects to
 * `/a/` when only `a/index.html` exists; `/a/` serves `a/index.html`, or redirects to `/a` when only `a.html` exists.
 */
function builtFile(pathname) {
  const p = decodeURIComponent(pathname)
  const candidates = p.endsWith('/')
    ? [`${p}index.html`, `${p.slice(0, -1)}.html`]
    : [p, `${p}.html`, `${p}/index.html`]
  return candidates.map((c) => path.join(dist, c)).find((f) => fs.existsSync(f) && fs.statSync(f).isFile())
}

const idCache = new Map()
function idsOf(file) {
  if (!idCache.has(file)) {
    const html = fs.readFileSync(file, 'utf8')
    idCache.set(file, new Set([...html.matchAll(/\s(?:id|name)="([^"]*)"/g)].map((m) => decodeEntities(m[1]))))
  }
  return idCache.get(file)
}

if (!fs.existsSync(dist)) {
  console.error(`${path.relative(process.cwd(), dist)} doesn't exist; run \`npm run build\` first.`)
  process.exit(1)
}

const files = walk(dist)
const pages = files.filter((f) => f.endsWith('.html'))
const problems = []
let checked = 0

for (const file of pages) {
  const pageName = path.relative(dist, file)
  const pageUrl = new URL(`/${pageName.replace(/(^|\/)index\.html$/, '$1').replace(/\.html$/, '')}`, siteOrigin)
  // Inline scripts carry JSON with markup in strings; only the tags themselves are links.
  const html = fs
    .readFileSync(file, 'utf8')
    .replace(/<script\b([^>]*)>[\s\S]*?<\/script>/gi, '<script$1></script>')
    .replace(/<!--[\s\S]*?-->/g, '')

  for (const [, tagName, attrs] of html.matchAll(/<([a-z][a-z\d-]*)\b([^>]*)>/gi)) {
    const tag = tagName.toLowerCase()
    for (const [, attr, raw] of attrs.matchAll(/\s(href|src|srcset|poster|data)="([^"]*)"/gi)) {
      const values = attr.toLowerCase() === 'srcset' ? raw.split(',').map((s) => s.trim().split(/\s+/)[0]) : [raw]
      for (const value of values.map(decodeEntities)) {
        if (value === '' || /^(mailto|tel):/i.test(value)) continue
        checked++
        const url = new URL(value, pageUrl)
        const where = `${pageName}: <${tag} ${attr}="${value}">`

        if (url.origin !== siteOrigin) {
          if (tag !== 'a') {
            problems.push(`${where} loads from another site`)
            continue
          }
          const repoFile = url.href.match(repoFileRE)
          if (repoFile) {
            const [, kind, repoPath] = repoFile
            const target = path.join(repoRoot, decodeURIComponent(repoPath))
            const exists = fs.existsSync(target) && fs.statSync(target).isDirectory() === (kind === 'tree')
            if (!exists) problems.push(`${where}: ${repoPath} is not ${kind === 'tree' ? 'a directory' : 'a file'} in the repository`)
          }
          continue
        }

        const target = builtFile(url.pathname)
        if (!target) {
          problems.push(`${where}: no built file for ${url.pathname}`)
          continue
        }
        const fragment = decodeURIComponent(url.hash.slice(1))
        if (fragment && target.endsWith('.html') && !idsOf(target).has(fragment)) {
          problems.push(`${where}: ${path.relative(dist, target)} has no element with id "${fragment}"`)
        }
      }
    }
  }
}

for (const file of files.filter((f) => f.endsWith('.css'))) {
  for (const [, ref] of fs.readFileSync(file, 'utf8').matchAll(/url\(\s*['"]?([^'")]+)/g)) {
    if (/^(https?:)?\/\//i.test(ref)) problems.push(`${path.relative(dist, file)}: url(${ref}) loads from another site`)
  }
}

const sitemap = path.join(dist, 'sitemap.xml')
if (!fs.existsSync(sitemap)) problems.push('sitemap.xml is missing')
else {
  for (const [, loc] of fs.readFileSync(sitemap, 'utf8').matchAll(/<loc>([^<]+)<\/loc>/g)) {
    checked++
    const url = new URL(loc)
    if (url.origin !== siteOrigin || !builtFile(url.pathname)) problems.push(`sitemap.xml: no built page for ${loc}`)
  }
}

const relayList = path.join(dist, 'relays.json')
if (!fs.existsSync(relayList)) problems.push('relays.json is missing')
else if (!fs.readFileSync(relayList).equals(fs.readFileSync(path.join(repoRoot, 'relays.json')))) {
  problems.push('relays.json differs from the repository root relays.json')
}

if (problems.length > 0) {
  console.error(problems.join('\n'))
  console.error(`\n${problems.length} problem(s) in ${checked} links across ${pages.length} pages.`)
  process.exit(1)
}
console.log(
  `All ${checked} links across ${pages.length} pages resolve; nothing loads from another site; ` +
    'relays.json equals the repository root relays.json.',
)
