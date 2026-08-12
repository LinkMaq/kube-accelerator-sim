#!/usr/bin/env node

import { readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { resolveDocumentationReleaseVersion } from './documentation-release.mjs'

const repositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
)
const releaseVersion = resolveDocumentationReleaseVersion({ repositoryRoot })
const expectedLink =
  `https://github.com/LinkMaq/kube-accelerator-sim/releases/tag/${releaseVersion}`
const pages = [
  path.join(repositoryRoot, 'docs/.vitepress/dist/index.html'),
  path.join(repositoryRoot, 'docs/.vitepress/dist/zh/index.html'),
]

for (const page of pages) {
  const html = readFileSync(page, 'utf8')
  if (!html.includes(expectedLink) || !html.includes(`>${releaseVersion}<`)) {
    throw new Error(
      `${path.relative(repositoryRoot, page)} does not advertise ${releaseVersion}`,
    )
  }
}

console.log(
  `English and Chinese documentation navigation advertise ${releaseVersion}.`,
)
