import type { Theme } from 'vitepress'
// The default theme without its bundled Inter font: the site uses the system fonts and loads no font files.
import DefaultTheme from 'vitepress/theme-without-fonts'
import { h } from 'vue'
import { useCodeTabs } from './codeTabs'
import DraftBanner from './DraftBanner.vue'
import NotFound from './NotFound.vue'
import SiteLinks from './SiteLinks.vue'
import './custom.css'
import './home.css'

export default {
  extends: DefaultTheme,
  Layout: () => h(DefaultTheme.Layout, null, { 'doc-after': () => h(SiteLinks), 'not-found': () => h(NotFound) }),
  enhanceApp({ app }) {
    app.component('DraftBanner', DraftBanner)
  },
  setup() {
    useCodeTabs()
  },
} satisfies Theme
