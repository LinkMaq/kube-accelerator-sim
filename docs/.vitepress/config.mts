import { existsSync, statSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitepress'

const repository = 'https://github.com/LinkMaq/kube-accelerator-sim'
const repositoryRoot = path.resolve(
  fileURLToPath(new URL('../..', import.meta.url)),
)
const documentationRoot = path.join(repositoryRoot, 'docs')

const zhNav = [
  { text: '指南', link: '/zh/operators/quickstart' },
  { text: '场景', link: '/zh/operators/scenario-examples' },
  { text: '兼容性', link: '/zh/operators/kubernetes-compatibility' },
  { text: '设备档案', link: '/zh/operators/profile-evidence' },
  { text: '架构', link: '/zh/architecture' },
  { text: 'v0.1.0', link: `${repository}/releases/tag/v0.1.0` },
]

const zhSidebar = [
  {
    text: '从这里开始',
    items: [
      { text: '概览', link: '/zh/' },
      { text: '已有集群快速开始', link: '/zh/operators/quickstart' },
      { text: '运行时安装', link: '/zh/operators/runtime-installation' },
    ],
  },
  {
    text: '操作模拟场景',
    items: [
      { text: '场景示例', link: '/zh/operators/scenario-examples' },
      { text: '厂商与型号依据', link: '/zh/operators/profile-evidence' },
      { text: 'Kubernetes 兼容性', link: '/zh/operators/kubernetes-compatibility' },
      { text: '升级与回滚', link: '/zh/operators/upgrade-rollback' },
      { text: '故障排查与安全', link: '/zh/operators/troubleshooting-security' },
    ],
  },
  {
    text: '保真度与架构',
    items: [
      { text: '架构导读', link: '/zh/architecture' },
      { text: '产品规范（英文）', link: '/spec/v1' },
      { text: '保真模式（英文）', link: '/adr/0001-fidelity-modes-and-simulation-backends' },
      { text: '设备档案契约（英文）', link: '/adr/0002-vendor-profile-and-model-contract' },
      { text: '场景生命周期（英文）', link: '/adr/0003-revisioned-scenario-instance-contract' },
      { text: '扩展边界（英文）', link: '/adr/0007-deep-modules-and-extension-seams' },
      { text: 'Kasim UI 提案', link: '/zh/spec/kasim-ui' },
    ],
  },
  {
    text: '发布证据',
    items: [
      { text: '发布验证', link: '/zh/operators/release-verification' },
      { text: 'Kubelet 协议基准', link: '/zh/operators/kubelet-protocol-oracle' },
      { text: 'v1 最终审计', link: '/zh/operators/final-audit' },
    ],
  },
  {
    text: '参与贡献',
    items: [
      { text: '保持中英文档同步', link: '/zh/contributing/documentation' },
    ],
  },
]

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
  locales: {
    root: {
      label: 'English',
      lang: 'en-US',
    },
    zh: {
      label: '简体中文',
      lang: 'zh-CN',
      link: '/zh/',
      title: 'Kasim',
      description: '面向 Kubernetes 调度与平台集成测试的加速器容量模拟工具。',
      head: [
        ['meta', { property: 'og:title', content: 'Kasim 中文文档' }],
        [
          'meta',
          {
            property: 'og:description',
            content: '模拟容量，验证调度。',
          },
        ],
      ],
      themeConfig: {
        nav: zhNav,
        sidebar: zhSidebar,
        darkModeSwitchLabel: '外观',
        lightModeSwitchTitle: '切换到浅色主题',
        darkModeSwitchTitle: '切换到深色主题',
        sidebarMenuLabel: '菜单',
        returnToTopLabel: '返回顶部',
        langMenuLabel: '切换语言',
        skipToContentLabel: '跳转到正文',
        outline: { level: [2, 3], label: '本页目录' },
        editLink: {
          pattern: `${repository}/edit/main/docs/:path`,
          text: '在 GitHub 上编辑此页',
        },
        lastUpdated: { text: '最后更新' },
        docFooter: { prev: '上一页', next: '下一页' },
        footer: {
          message: '仅模拟控制平面资源，不提供真实加速器计算能力。',
          copyright: '基于 Apache-2.0 许可证发布。',
        },
      },
    },
  },
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
          { text: 'Kasim UI proposal', link: '/spec/kasim-ui' },
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
        locales: {
          zh: {
            translations: {
              button: {
                buttonText: '搜索',
                buttonAriaLabel: '搜索文档',
              },
              modal: {
                displayDetails: '显示详细列表',
                resetButtonTitle: '重置搜索',
                backButtonTitle: '关闭搜索',
                noResultsText: '没有找到相关结果',
                footer: {
                  selectText: '选择',
                  selectKeyAriaLabel: '回车',
                  navigateText: '导航',
                  navigateUpKeyAriaLabel: '向上',
                  navigateDownKeyAriaLabel: '向下',
                  closeText: '关闭',
                  closeKeyAriaLabel: 'Esc',
                },
              },
            },
          },
        },
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
