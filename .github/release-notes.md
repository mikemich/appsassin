<!-- Release notes template for .github/workflows/release.yml (this line is dropped; TAG and REPO placeholders are filled in). Edit the release afterwards to add highlights. -->
### Install

**Homebrew**
```bash
brew install mikemich/tap/appsassin
```
Upgrading: `brew upgrade appsassin`

**Direct download**
```bash
curl -fsSL https://github.com/{{REPO}}/releases/download/{{TAG}}/appsassin-macos.tar.gz | tar xz
mkdir -p ~/.local/bin && mv appsassin ~/.local/bin/
```
One universal binary for Apple Silicon and Intel, macOS 12+. Verify it with `appsassin-macos.tar.gz.sha256`.

**Full changelog:** https://github.com/{{REPO}}/commits/{{TAG}}
