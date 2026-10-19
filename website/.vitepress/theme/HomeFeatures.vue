<script setup lang="ts">
// The home page's feature cards: `features` from the page's frontmatter (icon, title, details, optional link and
// linkText). `icon` names one of the icons below. `details` is HTML, as in the theme's own feature cards.
import { useData } from 'vitepress'

const { frontmatter } = useData()

// 24×24 stroke icons, drawn in the text colour.
const icons: Record<string, string[]> = {
  // Two windows, one room.
  room: ['M3 6.5A2.5 2.5 0 0 1 5.5 4h9A2.5 2.5 0 0 1 17 6.5v6a2.5 2.5 0 0 1-2.5 2.5h-9A2.5 2.5 0 0 1 3 12.5z', 'M7 19h11.5a2.5 2.5 0 0 0 2.5-2.5V10', 'm8.5 7.5 3.5 2-3.5 2z'],
  // A file of one's own.
  copy: ['M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z', 'M14 3v5h5', 'm10 11.5 4 2.5-4 2.5z'],
  // An open lock.
  free: ['M5 11h14v10H5z', 'M8 11V7a4 4 0 0 1 7.5-2', 'M12 15v2'],
  // A relay you run.
  server: ['M4 4h16v6H4z', 'M4 14h16v6H4z', 'M8 7h.01', 'M8 17h.01'],
}
</script>

<template>
  <div v-if="frontmatter.features" class="features">
    <ul class="grid">
      <li v-for="feature in frontmatter.features" :key="feature.title" class="feature">
        <svg
          v-if="icons[feature.icon]"
          class="icon"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
          aria-hidden="true"
        >
          <path v-for="d in icons[feature.icon]" :key="d" :d="d" />
        </svg>
        <h2 class="title">{{ feature.title }}</h2>
        <p class="details" v-html="feature.details"></p>
        <a v-if="feature.link" class="link" :href="feature.link">{{ feature.linkText }}<span aria-hidden="true"> →</span></a>
      </li>
    </ul>
  </div>
</template>

<style scoped>
.features {
  padding: 0 24px;
}

.grid {
  display: grid;
  gap: 16px;
  margin: 0 auto;
  max-width: 1152px;
}

/* On a phone the icon sits beside the title; from 640px wide, above it. */
.feature {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  align-items: center;
  column-gap: 14px;
  border: 1px solid var(--vp-c-divider);
  border-radius: 14px;
  padding: 20px;
  background-color: var(--vp-c-bg-soft);
}

.icon {
  box-sizing: content-box;
  border-radius: 10px;
  padding: 9px;
  width: 22px;
  height: 22px;
  color: var(--vp-c-brand-1);
  background-color: var(--vp-c-brand-soft);
}

.title {
  font-size: 17px;
  font-weight: 600;
  line-height: 24px;
  color: var(--vp-c-text-1);
}

.details {
  flex-grow: 1;
  grid-column: 1 / -1;
  margin-top: 12px;
  font-size: 15px;
  line-height: 24px;
  color: var(--vp-c-text-2);
}

/* A room code doesn't break at its hyphen. */
.details :deep(code) {
  font-family: var(--vp-font-family-mono);
  font-size: 0.93em;
  white-space: nowrap;
  color: var(--vp-c-text-1);
}

.link {
  grid-column: 1 / -1;
  justify-self: start;
  align-self: flex-start;
  margin-top: 16px;
  border-radius: 4px;
  font-size: 15px;
  font-weight: 600;
  line-height: 24px;
  color: var(--vp-c-brand-1);
  text-decoration: underline;
  text-decoration-color: transparent;
  text-underline-offset: 3px;
  transition: text-decoration-color 0.25s;
}

.link:hover {
  text-decoration-color: currentColor;
}

.link:focus-visible {
  outline: 2px solid var(--vp-c-brand-1);
  outline-offset: 3px;
}

@media (min-width: 640px) {
  .features {
    padding: 0 48px;
  }

  .feature {
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    padding: 24px;
  }

  .title {
    margin-top: 18px;
  }

  .details {
    margin-top: 8px;
  }

  .grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (min-width: 960px) {
  .features {
    padding: 0 64px;
  }

  .grid {
    grid-template-columns: repeat(4, minmax(0, 1fr));
  }
}
</style>
