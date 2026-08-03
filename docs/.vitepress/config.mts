import { existsSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitepress'

const repository = 'https://github.com/LinkMaq/kube-accelerator-sim'
const repositoryRoot = path.resolve(
  fileURLToPath(new URL('../..', import.meta.url)),
)
const documentationRoot = path.join(repositoryRoot, 'docs')

function isPublishedDocumentation(resolvedPath: string) {
  const documentationPath = path
    .relative(documentationRoot, resolvedPath)
    .split(path.sep)
    .join('/')
  return (
    !documentationPath.startsWith('agents/') &&
    !documentationPath.startsWith('research/') &&
    documentationPath !== 'operators/requirement-traceability' &&
    documentationPath !== 'operators/requirement-traceability.md'
  )
}

function repositoryLinkPlugin(md: any) {
  const renderLinkOpen =
    md.renderer.rules.link_open ??
    ((tokens: any[], index: number, options: any, _env: any, self: any) =>
      self.renderToken(tokens, index, options))

  md.renderer.rules.link_open = (
    tokens: any[],
    index: number,
    options: any,
    env: any,
    self: any,
  ) => {
    const hrefIndex = tokens[index].attrIndex('href')
    if (hrefIndex >= 0) {
      const href = tokens[index].attrs[hrefIndex][1]
      const isRelative = !/^(?:[a-z][a-z\d+.-]*:|#|\/)/iu.test(href)

      if (isRelative && env?.path) {
        const sourcePath = path.isAbsolute(env.path)
          ? env.path
          : path.resolve(documentationRoot, env.path)
        const [targetPath, suffix = ''] = href.split(/(?=[?#])/u, 2)
        const resolvedPath = path.resolve(path.dirname(sourcePath), targetPath)

        if (
          resolvedPath.startsWith(`${documentationRoot}${path.sep}`) &&
          isPublishedDocumentation(resolvedPath)
        ) {
          const documentationPath = path
            .relative(path.dirname(sourcePath), resolvedPath)
            .split(path.sep)
            .join('/')
          tokens[index].attrSet(
            'href',
            `${documentationPath.startsWith('.') ? '' : './'}${documentationPath}${suffix}`,
          )
        } else {
          const repositoryPath = path
            .relative(repositoryRoot, resolvedPath)
            .split(path.sep)
            .join('/')

          if (!repositoryPath.startsWith('../')) {
            const view =
              existsSync(resolvedPath) && statSync(resolvedPath).isDirectory()
                ? 'tree'
                : 'blob'
            tokens[index].attrSet(
              'href',
              `${repository}/${view}/main/${repositoryPath}${suffix}`,
            )
            tokens[index].attrSet('target', '_blank')
            tokens[index].attrSet('rel', 'noreferrer')
          }
        }
      }
    }

    return renderLinkOpen(tokens, index, options, env, self)
  }
}

export default defineConfig({
  title: 'Kasim',
  description:
    'Source-backed accelerator capacity simulation for Kubernetes scheduling and platform integration tests.',
  lang: 'en-US',
  base: '/kube-accelerator-sim/',
  srcExclude: [
    'agents/**',
    'research/**',
    'operators/requirement-traceability.md',
  ],
  cleanUrls: true,
  ignoreDeadLinks: [
    (link) =>
      link.includes('/research/') ||
      link.endsWith('/requirement-traceability'),
  ],
  lastUpdated: true,
  sitemap: {
    hostname: 'https://linkmaq.github.io/kube-accelerator-sim/',
  },
  head: [
    ['link', { rel: 'icon', type: 'image/png', href: '/kube-accelerator-sim/kasim-logo.png' }],
    ['meta', { name: 'theme-color', content: '#07111f' }],
    ['meta', { property: 'og:type', content: 'website' }],
    ['meta', { property: 'og:title', content: 'Kasim Documentation' }],
    [
      'meta',
      {
        property: 'og:description',
        content: 'Simulate Capacity. Validate Scheduling.',
      },
    ],
  ],
  markdown: {
    config: repositoryLinkPlugin,
    lineNumbers: true,
  },
  themeConfig: {
    logo: '/kasim-logo.png',
    siteTitle: 'Kasim',
    nav: [
      { text: 'Guide', link: '/operators/quickstart' },
      { text: 'Scenarios', link: '/operators/scenario-examples' },
      { text: 'Compatibility', link: '/operators/kubernetes-compatibility' },
      { text: 'Profiles', link: '/operators/profile-evidence' },
      { text: 'Architecture', link: '/spec/v1' },
      { text: 'v0.1.0', link: `${repository}/releases/tag/v0.1.0` },
    ],
    sidebar: [
      {
        text: 'Start here',
        items: [
          { text: 'Overview', link: '/' },
          { text: 'Existing-cluster quickstart', link: '/operators/quickstart' },
          { text: 'Runtime installation', link: '/operators/runtime-installation' },
        ],
      },
      {
        text: 'Operate scenarios',
        items: [
          { text: 'Scenario examples', link: '/operators/scenario-examples' },
          { text: 'Vendor profile evidence', link: '/operators/profile-evidence' },
          { text: 'Kubernetes compatibility', link: '/operators/kubernetes-compatibility' },
          { text: 'Upgrade and rollback', link: '/operators/upgrade-rollback' },
          { text: 'Troubleshooting and security', link: '/operators/troubleshooting-security' },
        ],
      },
      {
        text: 'Fidelity and architecture',
        items: [
          { text: 'Product specification', link: '/spec/v1' },
          { text: 'Fidelity modes', link: '/adr/0001-fidelity-modes-and-simulation-backends' },
          { text: 'Profiles and contracts', link: '/adr/0002-vendor-profile-and-model-contract' },
          { text: 'Revisioned lifecycle', link: '/adr/0003-revisioned-scenario-instance-contract' },
          { text: 'Explicit targets and receipts', link: '/adr/0005-explicit-target-receipt-driven-cli' },
          { text: 'Extension seams', link: '/adr/0007-deep-modules-and-extension-seams' },
        ],
      },
      {
        text: 'Release evidence',
        items: [
          { text: 'Release verification', link: '/operators/release-verification' },
          { text: 'Kubelet protocol oracle', link: '/operators/kubelet-protocol-oracle' },
          { text: 'Final v1 audit', link: '/operators/final-audit' },
        ],
      },
      {
        text: 'Contribute',
        items: [
          { text: 'Keep documentation in sync', link: '/contributing/documentation' },
        ],
      },
    ],
    socialLinks: [{ icon: 'github', link: repository }],
    search: {
      provider: 'local',
      options: {
        async _render(src, env, md) {
          const html =
            typeof md.renderAsync === 'function'
              ? await md.renderAsync(src, env)
              : md.render(src, env)
          return html
        },
      },
    },
    outline: { level: [2, 3], label: 'On this page' },
    editLink: {
      pattern: `${repository}/edit/main/docs/:path`,
      text: 'Edit this page on GitHub',
    },
    lastUpdated: { text: 'Updated' },
    docFooter: { prev: 'Previous', next: 'Next' },
    footer: {
      message: 'Control-plane simulation only. No physical accelerator compute.',
      copyright: 'Released under the Apache-2.0 License.',
    },
  },
})
