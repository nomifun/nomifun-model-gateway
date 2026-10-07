# Supplemental frontend attribution

Only license/attribution text is retained here; no implementation source was copied.

| Locked dependency | Publisher source | Exact commit | License / modifications |
| --- | --- | --- | --- |
| UnoCSS 66.10.5 and same-version @unocss packages | https://github.com/unocss/unocss | ccb92ea634f4dbfae0a9d8d352fac11df382da65 (peeled v66.10.5) | MIT; original `packages-integrations/vscode/LICENSE` text retained, no content modification |
| number-precision 1.6.0 | https://github.com/nefe/number-precision | 6e721680ca116b5b2c3c03db2b36ac57358cd595 (npm gitHead) | MIT explicitly declared in publisher `package.json`; author attribution retained; upstream tree/archive has no standalone LICENSE |

UnoCSS root LICENSE is a symlink to the recorded file. Its npm monorepo packages
do not all ship that standalone text, so collection supplies this pinned copy.
number-precision's MIT declaration and publisher author `cam song` are preserved
in its attribution and in the aggregate package metadata. The aggregate contains
the standard MIT permission text from other MIT dependencies; no missing original
copyright statement or year is invented.

The collector also records optional native build packages not installed for this
platform. They are build tools and are not redistributed in the scratch runtime.
