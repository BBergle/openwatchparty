import { inBrowser, onContentUpdated } from 'vitepress'

// Code samples in several languages are tab groups (`languageTabs` in ../docs.mts). Choosing a tab selects the tab
// with the same title in every group, on this page and on the pages read later.
const storageKey = 'owp-code-tab'

/** Selects the tab titled `title` in every group that has one; the theme shows the block of the same index. */
function selectEverywhere(title: string) {
  for (const group of document.querySelectorAll('.vp-code-group')) {
    const inputs = Array.from(group.querySelectorAll<HTMLInputElement>(':scope > .tabs > input'))
    const index = inputs.findIndex((input) => input.nextElementSibling?.getAttribute('data-title') === title)
    const blocks = group.querySelector(':scope > .blocks')?.children
    if (index < 0 || !blocks) continue
    inputs[index].checked = true
    Array.from(blocks).forEach((block, i) => block.classList.toggle('active', i === index))
  }
}

export function useCodeTabs() {
  if (!inBrowser) return
  onContentUpdated(() => {
    let title: string | null = null
    try {
      title = localStorage.getItem(storageKey)
    } catch {} // storage blocked: a choice lasts for its page only
    if (title) selectEverywhere(title)
  })
  // `change`, not `click`: the arrow keys move between tabs too.
  window.addEventListener('change', (event) => {
    const input = event.target
    if (!(input instanceof HTMLInputElement) || !input.matches('.vp-code-group > .tabs > input')) return
    const title = input.nextElementSibling?.getAttribute('data-title')
    const group = input.closest('.vp-code-group')
    if (!title || !group) return
    try {
      localStorage.setItem(storageKey, title)
    } catch {}
    // Groups above change height: keep the one the reader is using where it was on screen.
    const top = group.getBoundingClientRect().top
    selectEverywhere(title)
    window.scrollBy({ top: group.getBoundingClientRect().top - top, behavior: 'instant' })
  })
}
