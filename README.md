# imgs2jxl

Convert PNG files in a directory to JPEG XL with `cjxl`, in parallel, with
atomic writes and safe restart.

Verified `.jxl` siblings are adopted instead of re-encoded. Leftover
`.jxl.partial` files are removed on inventory. By default the PNG is deleted
after a successful stamp; use `-keep-originals` to leave it. Progress goes to
stdout; a durable log is appended as `imgs2jxl.log` in the target folder.

## Requirements

- Go 1.27+
- [`cjxl`](https://github.com/libjxl/libjxl) and `jxlinfo` on `PATH`

## Install

```bash
go install deedles.dev/imgs2jxl/cmd/imgs2jxl@latest
```

## Usage

```bash
imgs2jxl -path /path/to/folder
```

Flags:

| Flag | Meaning |
| --- | --- |
| `-path` | Directory of PNGs (default: cwd) |
| `-effort` | `cjxl -e`, 1–10 |
| `-distance` | `cjxl -d`, 0–25 |
| `-lossless` | Force `-d 0` |
| `-workers` | Parallel encodes, 1–32 (default 8) |
| `-threads-per-worker` | `cjxl --num_threads`, 0–64 |
| `-keep-originals` | Leave PNG after a verified JXL |
| `-limit` | Max new encodes; `0` unlimited |
| `-skip-newer-than-seconds` | Skip PNGs younger than this (default 30) |

Interrupt with Ctrl-C / SIGTERM; in-flight work stops cleanly.

## Library

```go
import "deedles.dev/imgs2jxl"

err := imgs2jxl.Run(ctx, imgs2jxl.DefaultConfig())
```

`Config` fields match the CLI. `FailedError` means at least one file failed
after the run finished.

## License

MIT. See [LICENSE](LICENSE).
