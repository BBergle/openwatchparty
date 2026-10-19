// The repository documents the site renders, and how links between repository files become site links.
//
// Each page in `docs` is built from the file at `source`: the dynamic routes in `src/[page].md` and `src/[dir]/[page].md`
// include the file with VitePress's `<!--@include-->` at build time, so the site never holds a copy of the text. A
// missing file fails the build.
//
// Links in every page are written as links between repository files, as they work on GitHub. `repoLinks` turns them
// into site links: a link to a document in `docs` goes to its page; a link to any other repository path goes to the
// file on GitHub. The handwritten pages in `src/` use the same convention (`../../spec/SPEC.md`).
//
// Section references in the text (`§11.8`, `SPEC.md §7.5`, `Appendix A`) become links to their headings too
// (`sectionLinks`), while the files keep the plain text GitHub shows.

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import type { MarkdownRenderer } from 'vitepress'

export const repoRoot = fileURLToPath(new URL('../..', import.meta.url))
export const repoUrl = 'https://github.com/edde746/openwatchparty'
export const siteUrl = 'https://openwatch.party'

export type Group = 'Integrate' | 'Specification' | 'SDKs' | 'Relay' | 'Policies'

export interface Doc {
  /** Repository path of the markdown file. */
  source: string
  /** Site path without the leading slash: one segment, or two (`docs/protocol`). */
  route: string
  title: string
  description: string
  group: Group
}

export const docs: Doc[] = [
  {
    source: 'docs/integration.md',
    route: 'docs/integration',
    title: 'Integration guide',
    description: 'How to add Open Watch Party to an app.',
    group: 'Integrate',
  },
  {
    source: 'spec/SPEC.md',
    route: 'docs/protocol',
    title: 'Protocol',
    description: 'The Open Watch Party v1 wire protocol and relay behaviour.',
    group: 'Specification',
  },
  {
    source: 'spec/SDK.md',
    route: 'docs/client',
    title: 'Client behaviour and SDK API',
    description: 'What an Open Watch Party client does, and the API every SDK implements.',
    group: 'Specification',
  },
  {
    source: 'sdk/typescript/README.md',
    route: 'sdk/typescript',
    title: 'TypeScript',
    description: 'The Open Watch Party SDK for browsers and Node.',
    group: 'SDKs',
  },
  {
    source: 'sdk/dart/README.md',
    route: 'sdk/dart',
    title: 'Dart and Flutter',
    description: 'The Open Watch Party SDK for Dart and Flutter.',
    group: 'SDKs',
  },
  {
    source: 'sdk/kotlin/README.md',
    route: 'sdk/kotlin',
    title: 'Kotlin Multiplatform',
    description: 'The Open Watch Party SDK for Android, the JVM and Apple platforms.',
    group: 'SDKs',
  },
  {
    source: 'sdk/swift/README.md',
    route: 'sdk/swift',
    title: 'Swift',
    description: 'The Open Watch Party SDK for Apple platforms and Linux.',
    group: 'SDKs',
  },
  {
    source: 'relay/README.md',
    route: 'relay',
    title: 'Relay and self-hosting',
    description: 'The Open Watch Party relay, and how to run your own.',
    group: 'Relay',
  },
  {
    source: 'docs/terms.md',
    route: 'terms',
    title: 'Terms of use',
    description: 'Terms of use for the Open Watch Party public relay.',
    group: 'Policies',
  },
  {
    source: 'docs/privacy.md',
    route: 'privacy',
    title: 'Privacy notice',
    description: 'How the Open Watch Party public relay handles personal data.',
    group: 'Policies',
  },
  {
    source: 'SECURITY.md',
    route: 'security',
    title: 'Security policy',
    description: 'How to report a vulnerability in Open Watch Party.',
    group: 'Policies',
  },
]

/** Marks a placeholder the owner still has to fill in, in the legal drafts. */
const ownerInputMarker = '[OWNER INPUT:'
/** A placeholder still open: the marker, except where a draft note shows its generic form `[OWNER INPUT: …]`. */
const openOwnerInputRE = /\[OWNER INPUT:(?! …\])/g

export interface PublishedDoc extends Doc {
  /** Number of `[OWNER INPUT: …]` placeholders left in the file. A page with any is shown as a draft. */
  ownerInputs: number
  /** Whether the file opens with its own `# ` heading. The package READMEs don't: npm and pub.dev name the package. */
  hasHeading: boolean
}

/** Every document, in `docs` order, with its open placeholders counted. */
export function published(): PublishedDoc[] {
  return docs.map((doc) => {
    const text = fs.readFileSync(path.join(repoRoot, doc.source), 'utf8')
    return { ...doc, ownerInputs: text.match(openOwnerInputRE)?.length ?? 0, hasHeading: text.startsWith('# ') }
  })
}

/** The document's title in the sidebar, footer and window title: marked as a draft while placeholders are open. */
export function label(doc: PublishedDoc): string {
  return doc.ownerInputs > 0 ? `${doc.title} (draft)` : doc.title
}

/** Paths for the dynamic route with one segment (`src/[page].md`) or two (`src/[dir]/[page].md`). */
export function routePaths(segments: 1 | 2) {
  const srcDir = path.join(repoRoot, 'website/src')
  return published()
    .filter((doc) => doc.route.split('/').length === segments)
    .map((doc) => {
      const [dir, page] = segments === 2 ? doc.route.split('/') : [undefined, doc.route]
      const frontmatter = {
        title: label(doc),
        description: doc.description,
        source: doc.source,
        ownerInputs: doc.ownerInputs,
      }
      // A draft gets <DraftBanner> (.vitepress/theme) above the document, inside the page's main content.
      const banner = doc.ownerInputs > 0 ? '<DraftBanner />\n\n' : ''
      // A document without its own heading gets its title as the page heading.
      const heading = doc.hasHeading ? '' : `# ${doc.title}\n\n`
      // `v-pre`: the documents are plain markdown, so Vue must not read `{{ }}` or tags in them as template syntax.
      const include = path.relative(srcDir, path.join(repoRoot, doc.source)).split(path.sep).join('/')
      const body = `<div v-pre>\n\n<!--@include: @/${include}-->\n\n</div>\n`
      const content = `---\n${JSON.stringify(frontmatter)}\n---\n\n${banner}${heading}${body}`
      return { params: segments === 2 ? { dir, page } : { page }, content }
    })
}

declare global {
  /**
   * `routePaths` from the docs.mts the site config loaded (config.mts sets it), for the paths files. VitePress keeps the
   * modules a paths file first loaded for the life of the dev server process, so a paths file importing docs.mts itself
   * keeps the docs list the server started with. The config, docs.mts with it, is loaded again on every change to
   * either, before VitePress asks the paths files for their pages.
   */
  var owpRoutePaths: typeof routePaths
}

/**
 * Heading IDs as GitHub makes them, so that `#anchors` written for GitHub work on the site too: lower case, every
 * character except letters, marks, numbers, `_`, `-` and spaces removed, spaces turned into `-`.
 */
export function githubSlug(text: string): string {
  return text
    .trim()
    .toLowerCase()
    .replace(/[^\p{L}\p{M}\p{N}\p{Pc}\- ]/gu, '')
    .replace(/ /g, '-')
}

interface Env {
  relativePath: string
  frontmatter?: { source?: string }
  /** Set while `sectionIds` parses a document only to read its heading IDs. */
  owpSectionScan?: true
}

/** Resolves a link in the markdown file at repository path `page`: the new href, or `undefined` to keep the link. */
export function resolveRepoLink(page: string, href: string, sourced: boolean): string | undefined {
  if (href === '' || href.startsWith('#') || href.startsWith('//') || /^[a-z][a-z\d+.-]*:/i.test(href)) return
  const [, target, suffix] = href.match(/^([^?#]*)(.*)$/)!
  // On GitHub a link starting with `/` is relative to the repository root; on a handwritten page it is a site path.
  if (target.startsWith('/') && !sourced) return
  const repoPath = path.posix.normalize(
    target.startsWith('/') ? target.slice(1) || '.' : path.posix.join(path.posix.dirname(page), target),
  ).replace(/(.)\/$/, '$1')
  if (repoPath.startsWith('../') || repoPath === '..') return
  if (repoPath.startsWith('website/src/')) return
  const doc = docs.find((d) => d.source === repoPath)
  if (doc) return `/${doc.route}${suffix}`
  if (repoPath === '.') return `${repoUrl}${suffix}`
  const file = path.join(repoRoot, repoPath)
  const kind = fs.existsSync(file) && fs.statSync(file).isDirectory() ? 'tree' : 'blob'
  return `${repoUrl}/${kind}/main/${repoPath}${suffix}`
}

/** markdown-it plugin: rewrites links between repository files (see the top of this file). */
export function repoLinks(md: MarkdownRenderer) {
  md.core.ruler.push('owp_repo_links', (state) => {
    const env = state.env as Env
    const source = env.frontmatter?.source
    const page = source ?? `website/src/${env.relativePath}`
    for (const block of state.tokens) {
      if (block.type !== 'inline' || !block.children) continue
      for (const token of block.children) {
        if (token.type !== 'link_open') continue
        const href = resolveRepoLink(page, token.attrGet('href') ?? '', source !== undefined)
        if (href) token.attrSet('href', href)
      }
    }
  })
}

/** The documents with numbered headings (`## 12. Playback…`, `### 12.1 Phases`, `## Appendix A. Error codes`). */
const numberedDocs = ['spec/SPEC.md', 'spec/SDK.md']

/** A section reference as the documents write it: `§12`, `§12.1` or `Appendix A`. */
const sectionRefRE = /§(\d+(?:\.\d+)*)|\bAppendix [A-Z]\b/g
/** A numbered heading's text: its number, or `Appendix A`, then a space. */
const numberedHeadingRE = /^(?:(\d+(?:\.\d+)*)\.?|(Appendix [A-Z])\.?)\s/
/** The names a reference may follow to point into a numbered document: `SPEC.md` or `spec/SPEC.md`. */
const docNames = new Map(numberedDocs.flatMap((source) => [[source, source], [path.posix.basename(source), source]]))
const escaped = [...docNames.keys()].map((name) => name.replace(/[./]/g, '\\$&'))
/** Text ending in a document name right before a reference: `… (SPEC.md ` */
const namedRE = new RegExp(`(?:^|[^\\w./-])(${escaped.join('|')})[ \\u00a0]+$`)
/** Text between two references of one list or range (`§7.5, §16.4`, `§9.3 and §9.4`, `§12.1–§12.3`). */
const continuationRE = /^(?:[ \u00a0]*[–-][ \u00a0]*|,[ \u00a0]+(?:(?:and|or)[ \u00a0]+)?|[ \u00a0]+(?:and|or)[ \u00a0]+)$/

const sectionCache = new Map<string, { mtimeMs: number; ids: Map<string, string> }>()

/**
 * The heading IDs of a numbered document by section (`12.1`, `Appendix A`), as the site renders them: the document is
 * parsed with the site's own markdown renderer, so the IDs come from its slugger, duplicates included.
 */
function sectionIds(md: MarkdownRenderer, source: string): Map<string, string> {
  const file = path.join(repoRoot, source)
  const { mtimeMs } = fs.statSync(file)
  const cached = sectionCache.get(source)
  if (cached?.mtimeMs === mtimeMs) return cached.ids
  const env: Env = { relativePath: '', frontmatter: { source }, owpSectionScan: true }
  const tokens = md.parse(fs.readFileSync(file, 'utf8'), env)
  const ids = new Map<string, string>()
  tokens.forEach((token, i) => {
    const id = token.attrGet('id')
    const heading = token.type === 'heading_open' ? numberedHeadingRE.exec(tokens[i + 1].content) : null
    if (heading && id) ids.set(heading[1] ?? heading[2], id)
  })
  sectionCache.set(source, { mtimeMs, ids })
  return ids
}

/**
 * markdown-it plugin: section references in text become links to their headings.
 * - `SPEC.md §7.5`, `` `SPEC.md` §7.5 ``, `[SPEC.md](../spec/SPEC.md) §5.2` and `SPEC.md Appendix B` point into the
 *   named document (SPEC.md or SDK.md).
 * - A bare `§11.8` or `Appendix A` points into the page's own document when that is SPEC.md or SDK.md. In every other
 *   document it stays text: only a document name or a link right before it says which document it means.
 * - The references after one in a list or range (`SPEC.md §7.5, §16.4`, `§9.3 and §9.4`, `§12.1–§12.3`) point into
 *   the same document.
 * Code, link text and headings are left alone. A reference to a section the document doesn't have fails the build.
 */
export function sectionLinks(md: MarkdownRenderer) {
  md.core.ruler.push('owp_section_links', (state) => {
    const env = state.env as Env
    if (env.owpSectionScan) return
    const page = env.frontmatter?.source ?? `website/src/${env.relativePath}`
    const own = numberedDocs.includes(page) ? page : undefined
    type Token = (typeof state.tokens)[number]
    const route = (source: string) => docs.find((doc) => doc.source === source)!.route

    // The numbered document named by the token right before a reference: inline code `SPEC.md`, or a link to its page
    // (`repoLinks` has already turned the link into `/docs/protocol…`).
    const namedBy = (before: Token[]) => {
      const last = before.at(-1)
      if (last?.type === 'code_inline') return docNames.get(last.content)
      if (last?.type !== 'link_close') return
      let nested = 0
      for (let k = before.length - 2; k >= 0; k--) {
        if (before[k].type === 'link_close') nested++
        else if (before[k].type === 'link_open' && nested-- === 0) {
          const href = (before[k].attrGet('href') ?? '').split('#')[0]
          return numberedDocs.find((source) => href === `/${route(source)}`)
        }
      }
    }
    const sectionHref = (target: string, ref: string) => {
      const id = sectionIds(state.md, target).get(ref.replace(/^§/, ''))
      if (!id) throw new Error(`${page}: "${ref}" names a section that ${target} doesn't have`)
      return target === page ? `#${id}` : `/${route(target)}#${id}`
    }

    state.tokens.forEach((block, i) => {
      if (block.type !== 'inline' || !block.children || state.tokens[i - 1]?.type === 'heading_open') return
      const out: Token[] = []
      const pushText = (content: string) => {
        if (content === '') return
        const piece = new state.Token('text', '', 0)
        piece.content = content
        out.push(piece)
      }
      let inLink = 0
      for (const token of block.children) {
        if (token.type === 'link_open') inLink++
        if (token.type === 'link_close') inLink--
        if (token.type !== 'text' || inLink > 0) {
          out.push(token)
          continue
        }
        const text = token.content
        let flushed = 0 // `out` holds the text before this offset
        let refEnd = 0 // where the previous reference ends
        let previous: string | undefined | null = null // the previous reference's document; null before the first
        for (const match of text.matchAll(sectionRefRE)) {
          const gap = text.slice(refEnd, match.index)
          const named = namedRE.exec(gap)?.[1]
          let target: string | undefined
          if (previous !== null && continuationRE.test(gap)) target = previous
          else if (named) target = docNames.get(named)
          else if (refEnd === 0 && /^[ \u00a0]*$/.test(gap)) target = namedBy(out) ?? own
          else target = own
          previous = target
          refEnd = match.index + match[0].length
          if (!target) continue
          pushText(text.slice(flushed, match.index))
          const open = new state.Token('link_open', 'a', 1)
          open.attrSet('href', sectionHref(target, match[0]))
          out.push(open)
          pushText(match[0])
          out.push(new state.Token('link_close', 'a', -1))
          flushed = refEnd
        }
        if (flushed === 0) out.push(token)
        else pushText(text.slice(flushed))
      }
      block.children = out
    })
  })
}

/** markdown-it plugin: marks inline code holding an `[OWNER INPUT: …]` placeholder so the theme can highlight it. */
export function ownerInputs(md: MarkdownRenderer) {
  md.core.ruler.push('owp_owner_inputs', (state) => {
    for (const block of state.tokens) {
      for (const token of block.children ?? []) {
        if (token.type === 'code_inline' && token.content.startsWith(ownerInputMarker)) {
          token.attrJoin('class', 'owner-input')
        }
      }
    }
  })
}

/** The SDK languages, as documents label a code sample: a paragraph `Dart:` right before the block. */
const sampleLabel = /^(TypeScript|Dart|Kotlin|Swift):$/

/**
 * markdown-it plugin: consecutive labelled code samples become one tab group (VitePress's `::: code-group`), so a page
 * shows one language at a time; theme/codeTabs.ts selects the reader's language in every group. The documents keep
 * the labelled blocks GitHub shows.
 */
export function languageTabs(md: MarkdownRenderer) {
  md.core.ruler.push('owp_language_tabs', (state) => {
    const tokens = state.tokens
    // The language of a sample starting at token i (label paragraph, then fence), or null.
    const sampleAt = (i: number) => {
      const [open, inline, close, fence] = tokens.slice(i, i + 4)
      if (open?.type !== 'paragraph_open' || open.hidden || close?.type !== 'paragraph_close') return null
      if (fence?.type !== 'fence') return null
      return sampleLabel.exec(inline?.content ?? '')?.[1] ?? null
    }
    const out: typeof tokens = []
    for (let i = 0; i < tokens.length; ) {
      if (!sampleAt(i)) {
        out.push(tokens[i++])
        continue
      }
      const open = new state.Token('container_code-group_open', 'div', 1)
      const close = new state.Token('container_code-group_close', 'div', -1)
      open.block = close.block = true
      open.level = close.level = tokens[i].level
      out.push(open)
      for (let language = sampleAt(i); language; language = sampleAt(i)) {
        const fence = tokens[i + 3]
        fence.info = `${fence.info.trim()} [${language}]`
        out.push(fence)
        i += 4
      }
      out.push(close)
    }
    state.tokens = out
  })
}
