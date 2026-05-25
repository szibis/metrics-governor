import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

const config: Config = {
  title: 'metrics-governor',
  tagline: 'High-performance metrics governance proxy for OTLP and Prometheus Remote Write',
  favicon: 'img/logo.svg',

  url: 'https://metrics-governor.io',
  baseUrl: '/',

  organizationName: 'szibis',
  projectName: 'metrics-governor',

  onBrokenLinks: 'throw',

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    format: 'md',
    mermaid: true,
  },

  themes: [
    '@docusaurus/theme-mermaid',
    [
      '@easyops-cn/docusaurus-search-local',
      {
        hashed: true,
        language: ['en'],
        highlightSearchTermsOnTargetPage: true,
        explicitSearchResultPath: true,
      },
    ],
  ],

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          editUrl: 'https://github.com/szibis/metrics-governor/tree/main/website/',
        },
        blog: {
          showReadingTime: true,
          editUrl: 'https://github.com/szibis/metrics-governor/tree/main/website/',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    image: 'img/logo.svg',
    navbar: {
      title: 'metrics-governor',
      logo: {
        alt: 'metrics-governor Logo',
        src: 'img/logo.svg',
      },
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Docs',
        },
        {to: '/blog', label: 'Blog', position: 'left'},
        {to: '/playground', label: 'Playground', position: 'left'},
        {
          href: 'https://github.com/szibis/metrics-governor',
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
            {label: 'Getting Started', to: '/docs/getting-started/installation'},
            {label: 'Configuration', to: '/docs/getting-started/configuration'},
            {label: 'Production Guide', to: '/docs/operations/production-guide'},
          ],
        },
        {
          title: 'Governance',
          items: [
            {label: 'Cardinality Limits', to: '/docs/governance/limits'},
            {label: 'Processing Rules', to: '/docs/governance/processing-rules'},
            {label: 'Multi-Tenancy', to: '/docs/governance/tenant'},
          ],
        },
        {
          title: 'More',
          items: [
            {label: 'Blog', to: '/blog'},
            {label: 'Playground', to: '/playground'},
            {label: 'GitHub', href: 'https://github.com/szibis/metrics-governor'},
            {label: 'Releases', href: 'https://github.com/szibis/metrics-governor/releases'},
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} metrics-governor. Apache 2.0 License.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'yaml', 'json', 'go', 'protobuf', 'toml'],
    },
    colorMode: {
      defaultMode: 'light',
      disableSwitch: false,
      respectPrefersColorScheme: true,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
