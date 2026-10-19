<script setup lang="ts">
// The footer's links and notice at the end of every page with a sidebar, where the theme hides its footer. Without
// JavaScript the mobile menus don't open, so these links (with previous/next) are how to reach every other page.
import { useData } from 'vitepress'

const { theme } = useData()
</script>

<template>
  <nav v-if="theme.footer" class="site-links" aria-label="Site">
    <p v-html="theme.footer.message"></p>
    <p v-html="theme.footer.copyright"></p>
  </nav>
</template>

<style scoped>
.site-links {
  margin-top: 48px;
  border-top: 1px solid var(--vp-c-divider);
  padding-top: 24px;
  font-size: 14px;
  line-height: 24px;
  color: var(--vp-c-text-2);
}

.site-links p + p {
  margin-top: 8px;
}

.site-links :deep(a) {
  color: var(--vp-c-text-2);
  text-decoration-line: underline;
  text-underline-offset: 2px;
}

.site-links :deep(a:hover) {
  color: var(--vp-c-text-1);
}
</style>
