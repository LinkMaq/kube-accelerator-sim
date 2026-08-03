#!/usr/bin/env node

import { spawnSync } from 'node:child_process'

function argument(name) {
  const index = process.argv.indexOf(name)
  return index >= 0 ? process.argv[index + 1] : undefined
}

const base = argument('--base')
const head = argument('--head')

if (!base || !head) {
  console.error(
    'usage: node tools/check-docs-impact.mjs --base BASE_SHA --head HEAD_SHA',
  )
  process.exit(2)
}

const diff = spawnSync(
  'git',
  ['diff', '--name-only', '--diff-filter=ACMR', `${base}...${head}`],
  { encoding: 'utf8' },
)

if (diff.status !== 0) {
  process.stderr.write(diff.stderr)
  process.exit(diff.status ?? 1)
}

const files = diff.stdout
  .split('\n')
  .map((file) => file.trim())
  .filter(Boolean)

const productPatterns = [
  /^api\//u,
  /^cmd\//u,
  /^internal\//u,
  /^profiles\//u,
  /^examples\//u,
  /^charts\//u,
  /^config\/crd\//u,
  /^Dockerfile$/u,
  /^go\.(mod|sum)$/u,
]

function isProductChange(file) {
  if (file.endsWith('_test.go') || file.startsWith('internal/tools/')) {
    return false
  }
  return productPatterns.some((pattern) => pattern.test(file))
}

function isDocumentationContent(file) {
  return (
    (file.startsWith('docs/') && file.endsWith('.md')) ||
    file === 'README.md' ||
    file === 'CONTEXT.md' ||
    file === 'examples/README.md' ||
    file.startsWith('release/notes/')
  )
}

const productChanges = files.filter(isProductChange)
const documentationChanges = files.filter(isDocumentationContent)

console.log(`Changed files: ${files.length}`)
console.log(`Product-facing files: ${productChanges.length}`)
console.log(`Documentation files: ${documentationChanges.length}`)

if (productChanges.length > 0 && documentationChanges.length === 0) {
  console.error('\nProduct behavior changed without a documentation update:')
  for (const file of productChanges) {
    console.error(`  - ${file}`)
  }
  console.error(
    '\nUpdate canonical Markdown in the same pull request. See docs/contributing/documentation.md.',
  )
  process.exit(1)
}

console.log('Documentation impact check passed.')
