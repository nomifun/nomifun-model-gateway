# Supplemental frontend attribution

English | [简体中文](README.zh-CN.md)

For application setup and product screenshots, see the [gateway usage guide](../../README.md)
or the [console guide](../../console/README.md). This directory records supplemental
publisher attribution used when collecting third-party notices for distribution.

Only license/attribution text is retained here; no implementation source was copied.

| Locked dependency | Publisher source | Exact commit | License / modifications |
| --- | --- | --- | --- |
| UnoCSS 66.10.5 and same-version @unocss packages | [unocss/unocss](https://github.com/unocss/unocss) | `ccb92ea634f4dbfae0a9d8d352fac11df382da65` (peeled v66.10.5) | MIT; original `packages-integrations/vscode/LICENSE` text retained, no content modification |
| number-precision 1.6.0 | [nefe/number-precision](https://github.com/nefe/number-precision) | `6e721680ca116b5b2c3c03db2b36ac57358cd595` (npm gitHead) | MIT explicitly declared in publisher `package.json`; author attribution retained; upstream tree/archive has no standalone LICENSE |

UnoCSS root LICENSE is a symlink to the recorded file. Its npm monorepo packages
do not all ship that standalone text, so collection supplies this pinned copy.
number-precision's MIT declaration and publisher author `cam song` are preserved
in its attribution and in the aggregate package metadata. The aggregate contains
the standard MIT permission text from other MIT dependencies; no missing original
copyright statement or year is invented.

The collector also records optional native build packages not installed for this
platform. They are build tools and are not redistributed in the scratch runtime.

## Audit and collect notices

Use Node.js 24 or later and run the following from the repository root after
`npm ci --ignore-scripts` in `console/`:

```sh
node scripts/check-frontend-licenses.mjs .
node --test scripts/check-frontend-licenses.test.mjs
```

The [notice collector](../../scripts/collect-frontend-notices.mjs) reads the exact
console lockfile and installed publisher files. It audits the lockfile before
collecting LICENSE, COPYING and NOTICE texts. Its default output is
`.tools/frontend-license-notices.txt`; create the output directory first.

PowerShell:

```powershell
New-Item -ItemType Directory -Force .tools | Out-Null
node scripts/collect-frontend-notices.mjs
```

Bash:

```sh
mkdir -p .tools
node scripts/collect-frontend-notices.mjs
```

`npm run build` in `console/` also generates runtime dependency notices in
`console/dist/THIRD_PARTY_LICENSES.txt` and copies them into the tracked
`internal/console/assets/THIRD_PARTY_LICENSES.txt` embedded by Go. Docker packages
the broader publisher collection at `/licenses/frontend.txt`, supplemental
source documents at `/licenses/frontend-sources/`, Go notices at `/licenses/go/`,
and the project LICENSE and NOTICE at `/licenses/`.

Retain the required notices when distributing a binary or image. An allowlisted
license identifier does not replace source provenance and redistribution review.
See the [license audit](../../docs/security/license-audit.md), [repository rules](../../AGENTS.md),
[project LICENSE](../../LICENSE) and [NOTICE](../../NOTICE) for the full policy.
