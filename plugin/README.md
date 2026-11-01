# cpa-usage-ma (CLIProxyAPI v8 Plugin)

Local-first token usage dashboard for [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), shipped as a native plugin (`.dylib`/`.so`) running inside the CPA process.

- **Real-time collection**: receives every completed request through the `usage.handle` plugin callback without polling. Write-error handling is still incomplete; this is not a lossless audit log.
- **Embedded dashboard**: the [cpa-usage-ma](https://github.com/marven-buddy/cpa-usage-ma) shadcn/ui frontend is bundled and served under `/v0/resource/plugins/cpa-usage-ma/report`.
- **SQLite storage**: usage events are stored locally in `usage.sqlite`.

## Install via CPA Plugin Store

Add this registry to `cliproxyapi.conf`:

```yaml
plugins:
  enabled: true
  store-sources:
    - "https://raw.githubusercontent.com/marven-buddy/cpa-usage-ma/main/plugin-store/registry.json"
  configs:
    cpa-usage-ma:
      enabled: true
      db_path: "~/.cli-proxy-api/cpa-usage-ma/usage.sqlite"
```

Then install from the management panel: **Plugin Store → cpa-usage-ma → Install** (after the prepared version is merged to main and published as a GitHub Release).

## Manual install

```bash
go build -buildmode=c-shared -o cpa-usage-ma.dylib .
mkdir -p <plugins-dir>/<goos>/<goarch>
cp cpa-usage-ma.dylib <plugins-dir>/<goos>/<goarch>/
```

## Dashboard

- Page: `/v0/resource/plugins/cpa-usage-ma/report`
- API: `/v0/management/plugins/cpa-usage-ma/api/usage?start=YYYY-MM-DD&end=YYYY-MM-DD&bucket=day|hour|month`

## Frontend assets

The files in `web/` are the build output of this repository's `frontend/` directory:

```sh
cd frontend && npm ci && npm run build
cp dist/index.html ../plugin/web/index.html
cp dist/assets/* ../plugin/web/assets/
```

After replacing assets, update the embedded file names and resource routes in `main.go` (build output uses hashed file names).

The data API requires `Authorization: Bearer <management-key>`. The independent plugin ID is `cpa-usage-ma`, with its own default database and browser storage. Original usage-report installations are not migrated automatically. See [release preparation](../RELEASING_CN.md) and [known limits](../README.md#已知限制).

## License

MIT. The bundled dashboard assets (`web/`) come from this repository's `frontend/` sources (MIT).
