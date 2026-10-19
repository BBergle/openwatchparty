<!-- The 404 page (the theme's `not-found` slot), in a main landmark. -->

<template>
  <main class="not-found">
    <p class="code">404</p>
    <h1>Page not found</h1>
    <p>There is no page at this address.</p>
    <p><a href="/">Go to the home page</a></p>
  </main>
</template>

<style scoped>
.not-found {
  padding: 64px 24px 96px;
  text-align: center;
  color: var(--vp-c-text-2);
}

@media (min-width: 768px) {
  .not-found {
    padding: 96px 32px 168px;
  }
}

.code {
  font-size: 64px;
  line-height: 64px;
  font-weight: 600;
  color: var(--vp-c-text-1);
}

h1 {
  margin: 12px 0 16px;
  font-size: 20px;
  font-weight: 700;
  color: var(--vp-c-text-1);
}

a {
  color: var(--vp-c-brand-1);
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
