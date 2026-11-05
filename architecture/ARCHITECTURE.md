# Architecture

| Principle | Meaning |
|---|---|
| Monolithic | one binary per customer project: admin, static build, endpoints |
| Go | single static binary, runs on any host, no runtime |
| Updatable | customer projects depend on baseplate as a Go module; update = version bump |
| Extensible | everything beyond the core comes as plugins (types, blocks, fields, pages, endpoints) |

```
customer project ── main.go ── baseplate + plugins ──▶ one binary ──▶ dist/ (static)
```

Details: [`docs/internals.md`](../docs/internals.md)
