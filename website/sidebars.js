// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docs: [
    'index',
    {
      type: 'category',
      label: 'Getting started',
      collapsed: false,
      link: { type: 'doc', id: 'getting-started/index' },
      items: [
        'getting-started/installation',
        'getting-started/quickstart',
      ],
    },
    {
      type: 'category',
      label: 'User guide',
      collapsed: false,
      link: { type: 'doc', id: 'user-guide/index' },
      items: [
        'user-guide/cli',
        'user-guide/mcp-tools',
        'user-guide/configuration',
        'user-guide/watch-mode',
        'user-guide/how-it-works',
      ],
    },
    {
      type: 'category',
      label: 'Architecture',
      collapsed: true,
      link: { type: 'doc', id: 'architecture/index' },
      items: [
        'architecture/modules',
        'architecture/platform',
      ],
    },
    {
      type: 'category',
      label: 'Extending Mekami',
      collapsed: true,
      link: { type: 'doc', id: 'extending/index' },
      items: [
        'extending/frontend-contract',
        'extending/writing-a-frontend',
        'extending/all-gen',
      ],
    },
    {
      type: 'category',
      label: 'Development',
      collapsed: true,
      link: { type: 'doc', id: 'development/index' },
      items: [
        'development/setup',
        'development/testing',
        'development/contributing',
        'development/releasing',
      ],
    },
    {
      type: 'category',
      label: 'Build & install',
      collapsed: true,
      link: { type: 'doc', id: 'build/index' },
      items: [
        'build/from-source',
        'build/aur',
      ],
    },
    {
      type: 'category',
      label: 'API reference',
      collapsed: true,
      link: { type: 'doc', id: 'api-reference/index' },
      items: [
        'api-reference/frontend-api',
      ],
    },
    'limitations',
    'license',
  ],
};

export default sidebars;
