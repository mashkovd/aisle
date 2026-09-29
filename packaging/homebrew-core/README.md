# homebrew-core formula

`aisle.rb` is the formula to submit to [homebrew-core](https://github.com/Homebrew/homebrew-core)
once the project is eligible (tracked in #28). It builds from source with Go, so it needs no
code signing or notarization, unlike a cask in the official Homebrew/cask tap, which must pass
Gatekeeper since 2026-09-01.

Eligibility for a self-submitted project ([Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy)):
at least 225 stars, 90 forks or 90 watchers, and a repository at least 30 days old.

Before submitting, bump `url` and `sha256` to the latest tag and check it the way it was checked for v0.4.0:

```sh
brew tap-new --no-git mashkovd/aisle-core-test
cp packaging/homebrew-core/aisle.rb "$(brew --repository)/Library/Taps/mashkovd/homebrew-aisle-core-test/Formula/"
brew style mashkovd/aisle-core-test/aisle
brew audit --new --strict --online --formula mashkovd/aisle-core-test/aisle
brew install --build-from-source --formula mashkovd/aisle-core-test/aisle && brew test mashkovd/aisle-core-test/aisle
brew uninstall --formula mashkovd/aisle-core-test/aisle && brew untap mashkovd/aisle-core-test
brew reinstall --cask mashkovd/tap/aisle   # the formula takes over the cask's links
```

The cask in `mashkovd/homebrew-tap` stays the install path until then; third-party taps are not
subject to the Gatekeeper rule.
