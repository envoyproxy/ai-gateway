import {themes as prismThemes} from 'prism-react-renderer';
import type {Config} from '@docusaurus/types';
import type * as Preset from '@docusaurus/preset-classic';

// This runs in Node.js - Don't use client-side code here (browser APIs, JSX...)

const config: Config = {
  title: 'Envoy AI Gateway',
  tagline: 'Envoy AI Gateway is an open source project for using Envoy Gateway to handle request traffic from application clients to GenAI services.',
  favicon: 'img/favicon.ico',

  // Set the production url of your site here
  url: 'https://envoyaigateway.io',
  // Set the /<baseUrl>/ pathname under which your site is served
  // For GitHub pages deployment, it is often '/<projectName>/'
  baseUrl: '/',

  // GitHub pages deployment config.

  organizationName: 'envoyproxy',
  projectName: 'ai-gateway',

  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'warn',

  // Even if you don't use internationalization, you can use this field to set
  // useful metadata like html lang. For example, if your site is Chinese, you
  // may want to replace "en" with "zh-Hans".
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  markdown: {
    mermaid: true,
  },

  themes: ['@docusaurus/theme-mermaid'],

  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: './sidebars.ts',
          remarkPlugins: [
            [require('@docusaurus/theme-mermaid'), {}],
          ],
          lastVersion: '0.2',
          versions: {
            current: {
              label: 'latest',
              path: 'latest',
              banner: 'unreleased'
            },
            '0.2': {
              label: '0.2',
              path: '',
              banner: 'none'
            },
            '0.1': {
              label: '0.1',
              path: '0.1',
              banner: 'unmaintained',
            },
          },
        },
        blog: {
          path: 'blog',
          showReadingTime: true,
          feedOptions: {
            type: ['rss', 'atom'],
            xslt: true,
          },
          onInlineTags: 'warn',
          onInlineAuthors: 'warn',
          onUntruncatedBlogPosts: 'warn',
        },
        theme: {
          customCss: './src/css/custom.css',
        },
        // Will be passed to @docusaurus/plugin-google-gtag (only enabled when explicitly specified)
        gtag: {
          trackingID: 'G-DXJEH1ZRXX',
        },
      } satisfies Preset.Options,
    ],
  ],

  themeConfig: {
    colorMode:{
      disableSwitch:true,
    },
    image: 'img/social-card-envoy-ai-gw.png',
    navbar: {
      title: 'Envoy AI Gateway',
      logo: {
        alt: 'Envoy',
        src: 'img/logo-white.svg',
      },
      items: [
        {
          label: 'Release Notes',
          to: '/release-notes/',
          position: 'right',
        },
        {
          label: 'Community',
          position: 'right',
           items: [
             {
              label: 'Join us on Slack',
              href: 'https://envoyproxy.slack.com/archives/C07Q4N24VAA',
            },
             {
              label: 'Weekly Meeting Notes (Thursdays)',
              href: 'https://docs.google.com/document/d/10e1sfsF-3G3Du5nBHGmLjXw5GVMqqCvFDqp_O65B0_w/edit?tab=t.0',
            },
            {
              label: 'GitHub Discussions',
              href: 'https://github.com/envoyproxy/ai-gateway/issues?q=is%3Aissue+label%3Adiscussion',
            },
          ],
        },
        {
          label: 'Blog',
          to: '/blog',
          position: 'left',
        },
        {
          label: 'Docs',
           to: '/docs', // Path to your Overview page
           position: 'left',
         },
         {
          type: 'docsVersionDropdown',
        },
        {
          href: 'https://github.com/envoyproxy/ai-gateway',
          label: 'GitHub',
          position: 'right',
        }
      ],
    },
    footer: {
      style: 'light',
      links: [
        {
          title: 'Envoy Ecosystem',
          items: [
            {
              label: 'Gateway',
              href: 'https://gateway.envoyproxy.io',
            },
            {
              label: 'Proxy',
              href: 'https://envoyproxy.io',
            },
            {
              label: 'Mobile',
              href: 'https://envoymobile.io',
            },
          ],
        },
        {
          title: 'Community',
          items: [
            {
              label: 'Join us on Slack',
              href: 'https://communityinviter.com/apps/envoyproxy/envoy',
            },
            {
              label: 'Weekly Meeting (Thursdays)',
              href: 'https://zoom-lfx.platform.linuxfoundation.org/meeting/91546415944?password=61fd5a5d-41e9-4b0c-86ea-b607c4513e37',
            },
            {
              label: 'Meeting Notes',
              href: 'https://docs.google.com/document/d/10e1sfsF-3G3Du5nBHGmLjXw5GVMqqCvFDqp_O65B0_w/edit?tab=t.0',
            },
            {
              label: 'LinkedIn',
              href: 'https://www.linkedin.com/company/envoy-cloud-native',
            },
          ],
        },
        {
          title: 'More',
          items: [
            {
              label: 'Blog',
              to: '/blog',
            },
            {
              label: 'GitHub',
              href: 'https://github.com/envoyproxy/ai-gateway',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} Envoy AI Gateway`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
    },
  } satisfies Preset.ThemeConfig,
};

export default config;
