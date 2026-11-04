const {themes: prismThemes} = require('prism-react-renderer');

const config = {
  title: '合同智能审计工作台',
  tagline: '产品、工程与运维手册',
  url: 'http://localhost:3000',
  baseUrl: '/',
  onBrokenLinks: 'throw',
  i18n: {
    defaultLocale: 'zh-Hans',
    locales: ['zh-Hans'],
  },
  markdown: {
    mermaid: true,
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },
  plugins: [
    [
      '@docusaurus/plugin-content-docs',
      {
        id: 'handbook',
        path: '../docs',
        routeBasePath: '/',
        sidebarPath: './sidebars.js',
      },
    ],
  ],
  themes: [
    ['@docusaurus/theme-classic', {customCss: './src/css/custom.css'}],
    '@docusaurus/theme-mermaid',
  ],
  themeConfig: {
    colorMode: {
      defaultMode: 'light',
      respectPrefersColorScheme: false,
    },
    navbar: {
      title: '合同审计工作台',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'handbook',
          docsPluginId: 'handbook',
          position: 'left',
          label: '手册',
        },
        {
          href: 'https://github.com/Rupert-WLLP-Bai/contract-audit-workbench',
          label: '查看源码',
          position: 'right',
        },
      ],
    },
    footer: {
      links: [
        {
          title: '手册',
          items: [
            { label: '产品能力', to: '/product/capabilities' },
            { label: '本地开发', to: '/engineering/local-development' },
            { label: '部署模式', to: '/operations/deployment' },
          ],
        },
      ],
      copyright: '合同智能审计工作台 · 使用手册',
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.vsDark,
      additionalLanguages: ['bash', 'diff', 'json', 'sql', 'toml', 'yaml'],
    },
  },
};

module.exports = config;
