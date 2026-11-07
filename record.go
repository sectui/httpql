package httpql

import (
	"strings"
	"time"
)

// Record is what Caido's fields read of a request and its response. Raw bytes
// are read only when a query asks for them.
type Record struct {
	ID        int
	Method    string
	Host      string
	Port      int
	Path      string
	Query     string
	TLS       bool
	Created   time.Time
	Code      int
	Len       int64 // response body length
	Roundtrip int64 // milliseconds
	Marked    bool

	ReqRaw  func() string
	RespRaw func() string
}

// Ext is the extension of the path's last segment, with its dot.
func (r *Record) Ext() string {
	seg := r.Path[strings.LastIndex(r.Path, "/")+1:]
	if i := strings.LastIndex(seg, "."); i >= 0 {
		return seg[i:]
	}
	return ""
}

func call(f func() string) string {
	if f == nil {
		return ""
	}
	return f()
}

// Fields are Caido's fields of a request and its response, with row.id and
// row.marked, over the Record rec finds in an R. The map is the caller's to
// change.
func Fields[R any](rec func(R) *Record) map[string]Field[R] {
	return map[string]Field[R]{
		"req.method":     Text(func(r R) string { return rec(r).Method }),
		"req.host":       Text(func(r R) string { return rec(r).Host }),
		"req.port":       Int(func(r R) int64 { return int64(rec(r).Port) }),
		"req.path":       Text(func(r R) string { return rec(r).Path }),
		"req.query":      Text(func(r R) string { return rec(r).Query }),
		"req.ext":        Text(func(r R) string { return rec(r).Ext() }),
		"req.tls":        Bool(func(r R) bool { return rec(r).TLS }),
		"req.created_at": Time(func(r R) time.Time { return rec(r).Created }),
		"req.raw":        Text(func(r R) string { return call(rec(r).ReqRaw) }).Raw(),
		"req.len":        Int(func(r R) int64 { return int64(len(call(rec(r).ReqRaw))) }).Raw(),
		"resp.code":      Int(func(r R) int64 { return int64(rec(r).Code) }),
		"resp.len":       Int(func(r R) int64 { return rec(r).Len }),
		"resp.roundtrip": Int(func(r R) int64 { return rec(r).Roundtrip }),
		"resp.raw":       Text(func(r R) string { return call(rec(r).RespRaw) }).Raw(),
		"row.id":         Int(func(r R) int64 { return int64(rec(r).ID) }),
		"row.marked":     Bool(func(r R) bool { return rec(r).Marked }),
	}
}

// Records is the schema of plain Records: Caido's fields and nothing else.
func Records() *Schema[*Record] {
	return &Schema[*Record]{Fields: Fields(func(r *Record) *Record { return r })}
}
