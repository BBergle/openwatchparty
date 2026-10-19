<script setup lang="ts">
// The home page's hero: `hero` from the page's frontmatter (name, text, tagline, actions), with HomeHeroArt beside it.
import { useData } from 'vitepress'
import { VPButton } from 'vitepress/theme-without-fonts'
import HomeHeroArt from './HomeHeroArt.vue'

const { frontmatter } = useData()
</script>

<template>
  <div v-if="frontmatter.hero" class="hero">
    <div class="container">
      <div class="main">
        <h1 class="heading">
          <span class="name">
            <svg class="mark" viewBox="0 0 32 32" aria-hidden="true">
              <rect width="32" height="32" rx="7" fill="#3451b2" />
              <path d="M7 8v16l11-8z" fill="#fff" fill-opacity=".5" />
              <path d="M13 8v16l11-8z" fill="#fff" />
            </svg>
            {{ frontmatter.hero.name }}
          </span>
          <span class="text">{{ frontmatter.hero.text }}</span>
        </h1>
        <p class="tagline">{{ frontmatter.hero.tagline }}</p>
        <div class="actions">
          <VPButton
            v-for="action in frontmatter.hero.actions"
            :key="action.link"
            tag="a"
            size="big"
            :theme="action.theme"
            :text="action.text"
            :href="action.link"
          />
        </div>
      </div>
      <HomeHeroArt />
    </div>
  </div>
</template>

<style scoped>
.hero {
  /* Under the navigation bar, which is transparent at the top of the home page. */
  margin-top: calc(var(--vp-nav-height) * -1);
  padding: calc(var(--vp-nav-height) + 40px) 24px 40px;
  background:
    radial-gradient(60% 70% at 85% 30%, var(--vp-c-brand-soft), transparent),
    linear-gradient(to bottom, var(--vp-c-bg-alt), var(--vp-c-bg));
}

.container {
  display: grid;
  gap: 40px;
  margin: 0 auto;
  max-width: 1152px;
}

.heading {
  display: flex;
  flex-direction: column;
  gap: 16px;
}

.name {
  display: flex;
  align-items: center;
  gap: 10px;
  font-size: 18px;
  font-weight: 700;
  line-height: 28px;
  color: var(--vp-c-brand-1);
}

.mark {
  flex-shrink: 0;
  width: 28px;
  height: 28px;
}

.text {
  max-width: 600px;
  font-size: 36px;
  font-weight: 700;
  line-height: 44px;
  letter-spacing: -0.02em;
  text-wrap: balance;
  color: var(--vp-c-text-1);
}

.tagline {
  margin-top: 16px;
  max-width: 520px;
  font-size: 18px;
  line-height: 28px;
  color: var(--vp-c-text-2);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  margin-top: 28px;
}

.actions :deep(.VPButton:focus-visible) {
  outline: 2px solid var(--vp-c-brand-1);
  outline-offset: 3px;
}

/* Both buttons fit on one line of a phone. */
@media (max-width: 639px) {
  .actions :deep(.VPButton.big) {
    padding: 0 18px;
    font-size: 15px;
  }
}

@media (min-width: 640px) {
  .hero {
    padding: calc(var(--vp-nav-height) + 64px) 48px 56px;
  }

  .text {
    font-size: 48px;
    line-height: 56px;
  }

  .tagline {
    font-size: 20px;
    line-height: 32px;
  }
}

@media (min-width: 960px) {
  .hero {
    padding: calc(var(--vp-nav-height) + 72px) 64px 72px;
  }

  .container {
    grid-template-columns: minmax(0, 5fr) minmax(0, 6fr);
    align-items: center;
    gap: 48px;
  }

  .text {
    font-size: 44px;
    line-height: 52px;
  }
}

/* From here the second line holds "across different apps". */
@media (min-width: 1280px) {
  .text {
    font-size: 52px;
    line-height: 60px;
  }
}
</style>
