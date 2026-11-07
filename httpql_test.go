package httpql

import (
	"strings"
	"testing"
	"time"
)

// hit is a record with fields of its own, as an app has.
type hit struct {
	Record
	Inputs       []string
	Words, Lines int64
}

var schema = func() *Schema[*hit] {
	fs := Fields(func(h *hit) *Record { return &h.Record })
	fs["fuzz.input"] = Texts(func(h *hit) []string { return h.Inputs })
	fs["fuzz.words"] = Int(func(h *hit) int64 { return h.Words })
	fs["fuzz.lines"] = Int(func(h *hit) int64 { return h.Lines })
	return &Schema[*hit]{Fields: fs, Refused: map[string]string{"source": "every hit comes from the same run"}}
}()

func Parse(q string) (*Query[*hit], error) { return schema.Parse(q) }

func ParsePresets(q string, presets map[string]string) (*Query[*hit], error) {
	return schema.ParsePresets(q, presets)
}

var rec = &hit{
	Record: Record{
		ID: 3, Method: "GET", Host: "api.test", Port: 443, Path: "/admin/index.php", Query: "a=1",
		TLS: true, Code: 403, Len: 1234, Roundtrip: 80,
		Created: time.Date(2026, 11, 7, 12, 0, 0, 0, time.Local),
		ReqRaw:  func() string { return "GET /admin/index.php HTTP/1.1\r\nHost: api.test\r\n\r\n" },
		RespRaw: func() string { return "HTTP/1.1 403 Forbidden\r\n\r\nflag{nope}" },
	},
	Inputs: []string{"admin", "x"}, Words: 10, Lines: 2,
}

func TestMatch(t *testing.T) {
	for q, want := range map[string]bool{
		``:                                       true,
		`resp.code.eq:403`:                       true,
		`resp.code.ne:403`:                       false,
		`resp.code.gte:500 OR resp.len.gt:1000`:  true,
		`resp.code.gte:500 AND resp.len.gt:1000`: false,
		`req.path.cont:"ADMIN"`:                  true,
		`req.path.eq:"/ADMIN/index.php"`:         false,
		`req.ext.eq:".php"`:                      true,
		`req.path.like:"/admin/%.php"`:           true,
		`req.path.nlike:"%.asp"`:                 true,
		`req.host.regex:"^api\\."`:               true,
		`req.tls.eq:true`:                        true,
		`"flag{"`:                                true,
		`"absent"`:                               false,
		`resp.raw.cont:"forbidden" and req.method.eq:GET`:               true,
		`(resp.code.eq:200 OR resp.code.eq:403) AND NOT_A_CLAUSE.eq:1`:  false,
		`fuzz.input.eq:"admin"`:                                         true,
		`fuzz.input.ne:"admin"`:                                         false,
		`fuzz.input.ncont:"zzz"`:                                        true,
		`fuzz.words.lt:11 AND fuzz.lines.eq:2`:                          true,
		`row.id.eq:3`:                                                   true,
		`req.created_at.gt:"2026-11-07"`:                                true,
		`req.created_at.lt:"2026-11-07T11:59"`:                          false,
		`resp.code.eq:200 OR resp.code.eq:403 AND req.method.eq:"POST"`: false,
	} {
		query, err := Parse(q)
		if err != nil {
			if !strings.Contains(q, "NOT_A_CLAUSE") {
				t.Errorf("Parse(%q): %v", q, err)
			}
			continue
		}
		if got := query.Match(rec); got != want {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
}

func TestErrors(t *testing.T) {
	for q, msg := range map[string]string{
		`resp.code.eq:"403"`:    "not an integer",
		`resp.code.cont:4`:      "unknown operator",
		`source.eq:"x"`:         "not available",
		`req.created_at.gt:"x"`: "not a date",
		`req.created_at.eq:"x"`: "unknown operator",
		`preset:"x"`:            `no preset "x"`,
		`nonsense`:              "expected namespace",
		`resp.code.eq:403 AND`:  "unexpected end",
		`(resp.code.eq:403`:     `expected ")"`,
		`req.path.cont:"x`:      "unterminated",
		`req.nope.eq:"x"`:       "unknown field",
		`req.path.regex:"("`:    "missing closing",
	} {
		_, err := Parse(q)
		if err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("Parse(%q) = %v, want %q", q, err, msg)
		}
	}
}

func TestNeedsRaw(t *testing.T) {
	q, _ := Parse(`resp.code.eq:200`)
	r, _ := Parse(`resp.code.eq:200 OR "x"`)
	if q.NeedsRaw() || !r.NeedsRaw() {
		t.Fatal("NeedsRaw")
	}
}

func TestPresetsAndMarks(t *testing.T) {
	presets := map[string]string{
		"errors":   "resp.code.gte:500",
		"marked":   "row.marked.eq:true",
		"both":     `preset:"errors" AND preset:marked`,
		"loop":     `preset:"loop"`,
		"broken":   "resp.code.eq:",
		"is empty": " ",
	}
	r := &hit{Record: Record{Code: 503, Marked: true}}
	for q, want := range map[string]bool{
		`preset:"both"`: true,
		`preset:"errors" AND row.marked.eq:false`: false,
		`row.marked.ne:true OR preset:errors`:     true,
	} {
		p, err := ParsePresets(q, presets)
		if err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		if got := p.Match(r); got != want {
			t.Errorf("%s = %v", q, got)
		}
	}
	for q, msg := range map[string]string{
		`preset:"loop"`:     "goes in circles",
		`preset:"broken"`:   `preset "broken"`,
		`preset:"is empty"`: "is empty",
		`preset:"nope"`:     `no preset "nope"`,
	} {
		if _, err := ParsePresets(q, presets); err == nil || !strings.Contains(err.Error(), msg) {
			t.Errorf("%s: %v, want %q", q, err, msg)
		}
	}
}

func TestBare(t *testing.T) {
	s := &Schema[*Record]{Fields: Records().Fields, Bare: []string{"req.path"}}
	q, err := s.Parse(`"admin"`)
	if err != nil {
		t.Fatal(err)
	}
	if !q.Match(&Record{Path: "/ADMIN"}) || q.NeedsRaw() {
		t.Fatal("a lone string searches the Bare fields")
	}
	s.Bare = []string{"req.port"}
	if _, err := s.Parse(`"x"`); err == nil || !strings.Contains(err.Error(), "lone string") {
		t.Fatalf("a lone string over a number field: %v", err)
	}
}

func TestZeroTime(t *testing.T) {
	q, _ := Records().Parse(`req.created_at.lt:"2100-01-01"`)
	if q.Match(&Record{}) {
		t.Fatal("a record without a date matches no date clause")
	}
}
