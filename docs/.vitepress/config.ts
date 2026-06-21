import { defineConfig } from 'vitepress'

// https://vitepress.dev/reference/site-config
export default defineConfig({
  title: 'Shuttle',
  description:
    'Self-hosted, git-driven Infrastructure-as-Code deployment platform — a single Go binary that rolls IaC changes out to your own hosts over Docker Compose.',
  lang: 'en-US',

  // Project site at https://neikow.github.io/shuttle/
  base: '/shuttle/',

  // Treat the repo's CLAUDE.md and other non-doc markdown as non-pages.
  srcExclude: ['**/README.md'],

  lastUpdated: true,
  cleanUrls: true,
  ignoreDeadLinks: [
    // Localhost URLs are runtime instructions (the dev cluster), not links to
    // validate at build time.
    /^https?:\/\/localhost/,
  ],

  head: [
    ['meta', { name: 'theme-color', content: '#000000' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'Shuttle' }],
    [
      'meta',
      {
        property: 'og:description',
        content:
          'Self-hosted, git-driven IaC deployment platform with rollback, drift detection, and Caddy ingress.',
      },
    ],
  ],

  themeConfig: {
    // https://vitepress.dev/reference/default-theme-config
    nav: [
      { text: 'Home', link: '/' },
      { text: 'Quickstart', link: '/guide/quickstart' },
      { text: 'Guide', link: '/guide/getting-started' },
      { text: 'Reference', link: '/architecture' },
      {
        text: 'v0.4.0',
        items: [
          {
            text: 'Releases',
            link: 'https://github.com/neikow/shuttle/releases',
          },
          {
            text: 'Changelog',
            link: 'https://github.com/neikow/shuttle/commits/main',
          },
        ],
      },
    ],

    sidebar: [
      {
        text: 'Get started',
        items: [
          { text: 'What is Shuttle?', link: '/guide/getting-started' },
          { text: 'Quickstart (3 min)', link: '/guide/quickstart' },
        ],
      },
      {
        text: 'Go to production',
        items: [
          { text: 'Installation', link: '/guide/installation' },
          { text: 'Deploy to a real host', link: '/guide/first-deployment' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'Architecture', link: '/architecture' },
          { text: 'Configuration', link: '/configuration' },
          { text: 'IaC repository', link: '/iac-repo' },
          { text: 'Editor support', link: '/editor' },
          { text: 'HTTP API', link: '/http-api' },
          { text: 'Operations', link: '/operations' },
        ],
      },
    ],

    socialLinks: [
      { icon: 'github', link: 'https://github.com/neikow/shuttle' },
    ],

    search: {
      provider: 'local',
    },

    editLink: {
      pattern:
        'https://github.com/neikow/shuttle/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },

    footer: {
      message: 'Released under the repository license.',
      copyright: 'Copyright © 2026 Shuttle contributors',
    },
  },
})
