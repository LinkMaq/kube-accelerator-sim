import { execFileSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const semanticVersionTag =
  /^v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/u

const defaultRepositoryRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  '..',
)

function validateReleaseVersion(version, source) {
  if (!semanticVersionTag.test(version)) {
    throw new Error(`${source} is not a semantic release tag: ${version}`)
  }
  return version
}

export function resolveDocumentationReleaseVersion({
  environment = process.env,
  repositoryRoot = defaultRepositoryRoot,
} = {}) {
  const configuredVersion = environment.KASIM_DOCS_RELEASE_VERSION?.trim()
  if (configuredVersion) {
    return validateReleaseVersion(
      configuredVersion,
      'KASIM_DOCS_RELEASE_VERSION',
    )
  }

  try {
    const version = execFileSync(
      'git',
      ['describe', '--tags', '--abbrev=0', '--match', 'v[0-9]*'],
      {
        cwd: repositoryRoot,
        encoding: 'utf8',
        stdio: ['ignore', 'pipe', 'ignore'],
      },
    ).trim()
    return validateReleaseVersion(version, 'nearest Git release tag')
  } catch (error) {
    const detail = error instanceof Error ? `: ${error.message}` : ''
    throw new Error(
      'Unable to resolve the documentation release version. Fetch release tags or set KASIM_DOCS_RELEASE_VERSION' +
        detail,
    )
  }
}
