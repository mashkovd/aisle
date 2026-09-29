# Contributing

## Development

```sh
go test ./...          # tmux tests run against an isolated server and skip if tmux is absent
golangci-lint run
goreleaser release --snapshot --clean --skip=publish
```

## Demo GIF

`vhs demo/aisle.tape` re-records `demo/aisle.gif`. It runs on synthetic history from
`demo/seed.py` and stand-in agent binaries in `demo/bin/`, in a throwaway `$HOME`,
so nothing from your machine ends up in the recording.

## Adding or updating an agent format

Every adapter is tested against golden fixtures under `testdata/<engine>/<version>/`.
When an agent changes its storage format, add a new version directory instead of
editing the old one, so older installations keep being covered. Fixtures are
synthetic: never commit real prompts, paths or credentials.

An adapter must either read a file correctly or return a `Warning` with
`unsupported format`; it must never drop part of the history silently.

## Instruction-file rules

`internal/rules` encodes how each agent finds its instruction files. Every
scenario is a directory under `testdata/rules/<case>/` (`project/`, `home/`, an
optional `claude-version` and a `global` marker) with the expected report in
`want.txt`; `go test ./internal/rules -update` rewrites them — review the diff.
When you change what aisle believes about an agent, confirm it against the real
agent first (a codeword in a throwaway directory works well) or mark the finding
`Unverified`.

## Full-text index

Adapters feed the index through `adapter.Searchable`: `Transcripts` lists the
files, `Extract` emits conversation text. Index only what a person would search
for — prompts and visible replies, never tool output, thinking or injected
context. Mark a transcript `Append` only if the agent never rewrites it.

Whenever extraction or the index schema changes, bump `schemaVersion` in
`internal/search/index.go`: existing indexes are then dropped and rebuilt instead
of mixing old and new extraction.

## Releases

Tags are `vMAJOR.MINOR.PATCH` (for example `v0.1.0`). The `v` prefix is required
by Go module versioning (`go install …@v0.1.0`). Pushing a tag runs GoReleaser,
which publishes the GitHub release and updates the cask in `mashkovd/homebrew-tap`.
