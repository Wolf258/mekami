// @ts-check
// `@type` JSDoc annotations let editors provide autocompletion and type checking.
// (When running `docusaurus build` Docusaurus loads this file as ES module.)

import { themes as prismThemes } from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Mekami',
  tagline: 'A SQLite-backed Go code graph for humans and LLM agents.',

  url: 'https://wolf258.github.io',
  baseUrl: '/mekami/',

  organizationName: 'Wolf258',
  projectName: 'mekami',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          path: '../docs',
          routeBasePath: '/',
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/Wolf258/mekami/edit/main/',
          showLastUpdateAuthor: false,
          showLastUpdateTime: false,
        },
        blog: false,
      }),
    ],
  ],

  stylesheets: ['/css/custom.css'],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      colorMode: {
        defaultMode: 'light',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
      tableOfContents: {
        minHeadingLevel: 2,
        maxHeadingLevel: 4,
      },
      navbar: {
        title: 'Mekami',
        items: [
          {
            href: 'https://github.com/Wolf258/mekami',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              { label: 'Getting started', to: '/getting-started/installation' },
              { label: 'CLI reference', to: '/user-guide/cli' },
              { label: 'Architecture', to: '/architecture/' },
            ],
          },
          {
            title: 'Project',
            items: [
              { label: 'GitHub', href: 'https://github.com/Wolf258/mekami' },
              { label: 'Releasing', to: '/development/releasing' },
            ],
          },
          {
            title: 'More',
            items: [
              { label: 'AUR packaging', to: '/build/aur' },
              { label: 'License (MIT)', to: '/license' },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Wolf258. Released under the MIT License.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['bash', 'go', 'json', 'yaml', 'rust', 'toml'],
      },
    }),
};

export default config;
