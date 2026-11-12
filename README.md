# httpql

Caido's [HTTPQL](https://docs.caido.io/reference/httpql.html) for Go, as the
[foam](https://github.com/anotherhadi/foam) tools use it to filter requests and
responses:

```
resp.code.gte:500 AND req.path.cont:"admin"
resp.len.gt:1000 OR (resp.code.eq:200 AND req.ext.eq:".php")
"flag{"
```

The fields belong to the app: a `Schema` names them over the app's own record
type. `Fields` gives Caido's request and response fields over a `Record`, to
add the app's own to:

```go
type hit struct {
	httpql.Record
	Words int64
}

fields := httpql.Fields(func(h *hit) *httpql.Record { return &h.Record })
fields["fuzz.words"] = httpql.Int(func(h *hit) int64 { return h.Words })
schema := &httpql.Schema[*hit]{Fields: fields}

q, err := schema.ParsePresets(`preset:"errors" AND fuzz.words.gt:10`, map[string]string{
	"errors": "resp.code.gte:500",
})
q.Match(h)
```

Fields read from the raw messages (`req.raw`, `resp.raw`, `req.len`) are marked
with `Raw()`: `Query.NeedsRaw` tells when a query reads them, to compute it off
the UI thread.

| Type    | Operators                                                       |
| ------- | --------------------------------------------------------------- |
| number  | `eq`, `ne`, `gt`, `gte`, `lt`, `lte`                            |
| text    | `eq`, `ne`, `cont`, `ncont`, `like`, `nlike`, `regex`, `nregex` |
| boolean | `eq`, `ne`                                                      |
| date    | `gt`, `lt` (`"2026-11-07"`, `"2026-11-07T14:30"`, RFC 3339)     |
