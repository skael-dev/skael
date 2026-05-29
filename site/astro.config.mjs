// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import sitemap from '@astrojs/sitemap';
import starlightLlmsTxt from 'starlight-llms-txt';

// https://astro.build/config
export default defineConfig({
  site: 'https://skael.dev',
  integrations: [
    sitemap(),
    starlight({
      title: 'skael docs',
      plugins: [starlightLlmsTxt({ projectName: 'skael' })],
      // Custom landing page lives at "/"; docs are served from /docs/* because
      // content is nested under src/content/docs/docs/.
      logo: { src: './public/favicon.svg', alt: 'skael' },
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/skael-dev/skael' },
      ],
      customCss: ['./src/styles/docs.css'],
      head: [
        { tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.googleapis.com' } },
        { tag: 'link', attrs: { rel: 'preconnect', href: 'https://fonts.gstatic.com', crossorigin: true } },
        { tag: 'link', attrs: { rel: 'stylesheet', href: 'https://fonts.googleapis.com/css2?family=Geist:wght@100..900&family=Geist+Mono:wght@100..900&display=swap' } },
      ],
      sidebar: [
        { label: 'Start here', items: [
          { label: 'Overview', slug: 'docs' },
          { label: 'Quickstart', slug: 'docs/quickstart' },
          { label: 'Core concepts', slug: 'docs/concepts' },
        ] },
        { label: 'Reference', items: [
          { label: 'CLI', slug: 'docs/cli' },
          { label: 'Self-hosting', slug: 'docs/self-hosting' },
        ] },
        { label: 'Why skael', items: [
          { label: 'Why not just git?', slug: 'docs/why-not-git' },
        ] },
      ],
    }),
  ],
});
