<script setup lang="ts">
// The home page layout, in place of the theme's VPHome.vue (VitePress 1.6.4): a <main> landmark like every other page
// has, holding HomeHero and HomeFeatures (from the page's frontmatter) and then the page's own content, which
// home.css styles. config.mts swaps it in with a Vite alias, VitePress's documented way to override an internal
// component; compare it with the theme's VPHome.vue when upgrading VitePress.
import HomeFeatures from './HomeFeatures.vue'
import HomeHero from './HomeHero.vue'
</script>

<template>
  <main class="VPHome">
    <HomeHero />
    <HomeFeatures />
    <Content class="home-content" />
  </main>
</template>

<style scoped>
.VPHome {
  margin-bottom: 96px;
}

@media (min-width: 768px) {
  .VPHome {
    margin-bottom: 128px;
  }
}
</style>
