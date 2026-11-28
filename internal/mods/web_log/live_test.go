package weblog

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// encodePythonPickledString produces zlib.compress(pickle.dumps(s, protocol=2)) matching Python LocalListV3.rd_rpush().
func encodePythonPickledString(s string) string {
	var p bytes.Buffer
	p.Write([]byte{0x80, 0x02, 'X'}) // PROTO 2, BINUNICODE
	var n [4]byte
	binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
	p.Write(n[:])
	p.WriteString(s)
	p.WriteByte('.') // STOP

	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(p.Bytes())
	_ = w.Close()
	return z.String()
}

func TestParseLiveLogLineFormats(t *testing.T) {
	// Standard 6-segment rd_log line with host.
	e := ParseLiveLogLine("10.135.12.5 | 2026-11-28 09:15:02.123456 | IE: tab11 | 200 | info | sgmescimai")
	if e.IP != "10.135.12.5" || e.Timestamp != "2026-11-28 09:15:02.123456" || e.App != "IE" || e.Action != "tab11" || e.Status != "200" || e.Level != "info" || e.Host != "sgmescimai" {
		t.Errorf("6-segment parse mismatch: %+v", e)
	}

	// Message containing " | " stays intact because trailing status/level/host are stripped from the right.
	ePipe := ParseLiveLogLine("127.0.0.1 | 2026-11-28 09:16:00.000123 | API: job A | step B failed | 502 | error | prod_eng")
	if ePipe.App != "API" || ePipe.Action != "job A | step B failed" || ePipe.Status != "502" || ePipe.Level != "error" || ePipe.Host != "prod_eng" {
		t.Errorf("pipe-in-message parse mismatch: %+v", ePipe)
	}

	// Older 5-segment line without host.
	e5 := ParseLiveLogLine("10.1.2.3 | 2026-11-28 09:17:00.000001 | QDB: tab3 | 200 | warning")
	if e5.IP != "10.1.2.3" || e5.App != "QDB" || e5.Action != "tab3" || e5.Status != "200" || e5.Level != "warning" || e5.Host != "" {
		t.Errorf("5-segment parse mismatch: %+v", e5)
	}

	// Unstructured fallback line preserves raw text safely.
	eRaw := ParseLiveLogLine("arbitrary diagnostic message")
	if eRaw.Raw != "arbitrary diagnostic message" || eRaw.Message != "arbitrary diagnostic message" || eRaw.Level != "info" {
		t.Errorf("raw fallback mismatch: %+v", eRaw)
	}
}

func TestRedisLiveLogDecodesPickleAndReversesNewestFirst(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	l1 := "10.0.0.1 | 2026-11-28 10:00:01.000001 | IE: tab1 | 200 | info | prod_eng"
	l2 := "10.0.0.2 | 2026-11-28 10:00:02.000002 | QDB: tab3 | 500 | error | sgmescimai"
	l3 := "10.0.0.3 | 2026-11-28 10:00:03.000003 | MER: tab7 | 200 | warning | sgmescimemg"
	// Mix zlib+pickle (Python rd_rpush) and plain UTF-8 fallback.
	if err := rdb.RPush(ctx, "web_log",
		encodePythonPickledString(l1),
		encodePythonPickledString(l2),
		l3,
	).Err(); err != nil {
		t.Fatal(err)
	}

	store := NewRedisLiveLog(rdb, "web_log")
	entries, total, err := store.Recent(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 {
		t.Fatalf("total = %d, want 3", total)
	}
	if len(entries) != 2 {
		t.Fatalf("len = %d, want 2", len(entries))
	}
	// Newest entry first (l3, then l2).
	if entries[0].App != "MER" || entries[0].Host != "sgmescimemg" || entries[0].Level != "warning" || entries[0].Seq != 1 {
		t.Errorf("entries[0] = %+v", entries[0])
	}
	if entries[1].App != "QDB" || entries[1].Host != "sgmescimai" || entries[1].Level != "error" || entries[1].Status != "500" || entries[1].Seq != 2 {
		t.Errorf("entries[1] = %+v", entries[1])
	}
}
