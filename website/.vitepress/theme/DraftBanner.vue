<script setup lang="ts">
// Placed above a repository document that still holds `[OWNER INPUT: …]` placeholders (.vitepress/docs.mts). The count
// comes from the file at build time, so the banner goes away by itself once the last placeholder is filled in.
import { computed } from 'vue'
import { useData } from 'vitepress'

const { frontmatter } = useData()
const count = computed<number>(() => frontmatter.value.ownerInputs ?? 0)
</script>

<template>
  <div v-if="count > 0" class="draft-banner" role="note">
    <p>
      <strong>Draft, not in force.</strong>
      {{ count }} {{ count === 1 ? 'item is' : 'items are' }} still marked <code>[OWNER INPUT: …]</code> below.
    </p>
  </div>
</template>

<style scoped>
.draft-banner {
  margin: 0 0 24px;
  border: 1px solid var(--vp-c-warning-2);
  border-left-width: 6px;
  border-radius: 8px;
  padding: 12px 16px;
  background-color: var(--vp-c-warning-soft);
  color: var(--vp-c-text-1);
  font-size: 15px;
  line-height: 24px;
}

.draft-banner p {
  margin: 0;
}
</style>
