# Releasing

CI (`.github/workflows/ci.yml`) runs on every push to `main` and every pull
request: gofmt, `go vet`, `go test -race`, and a cross-compile of every release
target.

To publish a version:

```sh
git tag v1.2.0
git push origin v1.2.0
```

The release workflow then builds Linux and macOS (amd64 and arm64) binaries
with GoReleaser, and creates a GitHub Release with the archives, a
`checksums.txt` and a changelog. The newest release is the one GitHub shows in
the repository's sidebar. `yt-media version` prints the tag it was built from.

Version numbers: bump the patch (`v1.2.1`) for fixes, the minor (`v1.3.0`) for
new features, the major (`v2.0.0`) for breaking changes to the config file or
commands.

To try a release without publishing:

```sh
goreleaser release --snapshot --clean
```
