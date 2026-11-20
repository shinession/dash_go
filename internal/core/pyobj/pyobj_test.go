package pyobj

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
)

func vector(t *testing.T, name string) []byte {
	t.Helper()
	b, err := hex.DecodeString(vectors[name])
	if err != nil {
		t.Fatalf("vector %s: %v", name, err)
	}
	return b
}

func z(t *testing.T, raw []byte) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write(raw)
	_ = w.Close()
	return b.Bytes()
}

// The vectors were made by the real pickle and zlib of Python 3.12 the way pickle_dumps() of the dash application
// makes them (the default protocol is 4; protocols 2 and 5 are covered too).
func TestDecodeReadsTheSignOnResultOfEveryProtocol(t *testing.T) {
	want := map[string]any{"status": "ok", "ee_code": "AB12C34", "windows_user": `CO\AB12C34`, "ip": "10.1.2.3", "ts": 1700000123.456}
	for _, name := range []string{"ssoDefault", "ssoProto2", "ssoProto5"} {
		got, err := Decode(vector(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %#v", name, got)
		}
	}
}

func TestDecodePendingAndMixedValues(t *testing.T) {
	got, err := Decode(vector(t, "pending"))
	if err != nil || !reflect.DeepEqual(got, map[string]any{"status": "pending"}) {
		t.Errorf("pending: %#v, %v", got, err)
	}
	got, err = Decode(vector(t, "mixed"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"name": "陈小明", "n": int64(3), "none": nil, "flag": true}; !reflect.DeepEqual(got, want) {
		t.Errorf("mixed: %#v", got)
	}
	got, err = Decode(vector(t, "containers"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]any{"a": []any{int64(1), int64(2)}, "b": []any{int64(1), "x", 2.5}}; !reflect.DeepEqual(got, want) {
		t.Errorf("containers: %#v", got)
	}
}

func TestDecodeRefusesWhatIsNotPlainData(t *testing.T) {
	for _, name := range []string{"setValue", "datetimeValue"} {
		if _, err := Decode(vector(t, name)); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s: want ErrUnsupported, got %v", name, err)
		}
	}
}

// A pickle can call any function of the interpreter that loads it. The decoder must read this one as data and
// refuse it, without anything being called.
func TestDecodeNeverRunsCode(t *testing.T) {
	evil := [][]byte{
		[]byte("cos\nsystem\n(S'echo pwned'\ntR."),                       // protocol 0: os.system('echo pwned')
		[]byte("\x80\x02cbuiltins\neval\nX\x04\x00\x00\x001+1\x85R."),    // protocol 2: eval('1+1')
		[]byte("\x80\x04\x8c\x02os\x8c\x06system\x93\x8c\x04calc\x85R."), // protocol 4: STACK_GLOBAL + REDUCE
		[]byte("\x80\x02}(X\x01\x00\x00\x00ac__main__\nboom\n)Rus."),     // a call hidden in a dict value
		[]byte("i__main__\nboom\n(dp0\n."),                               // INST is not allowed at all
		[]byte("\x80\x02}(X\x01\x00\x00\x00aQus."),                       // persistent id
	}
	for i, p := range evil {
		v, err := Decode(z(t, p))
		if err == nil {
			t.Errorf("case %d decoded to %#v: a call or a class must not become plain data", i, v)
		}
	}
}

func TestDecodeBadInputIsAnErrorNotAPanic(t *testing.T) {
	good := vector(t, "ssoDefault")
	inputs := map[string][]byte{
		"empty":            nil,
		"not zlib":         []byte("hello"),
		"truncated zlib":   good[:len(good)/2],
		"truncated pickle": z(t, []byte("\x80\x02}(X\x06\x00\x00\x00st")),
		"unknown opcode":   z(t, []byte("\xfe.")),
		"no stop":          z(t, []byte("N")),
		"stack underflow":  z(t, []byte("\x80\x02u.")),
		"huge string size": z(t, []byte("\x80\x04\x8d\xff\xff\xff\xff\xff\xff\xff\x7f.")),
		"odd dict items":   z(t, []byte("\x80\x02}(NNNu.")),
		"invalid utf8":     z(t, []byte("\x80\x04\x8c\x02\xff\xfe.")),
		"memo of nothing":  z(t, []byte("\x80\x02h\x05.")),
		"non string key":   z(t, []byte("\x80\x02}(K\x01K\x02u.")),
	}
	for name, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: panic %v", name, r)
				}
			}()
			if v, err := Decode(in); err == nil {
				t.Errorf("%s: want an error, got %#v", name, v)
			}
		}()
	}
}

func TestDecodeStopsAZipBomb(t *testing.T) {
	bomb := z(t, bytes.Repeat([]byte{'N'}, 4<<20)) // 4 MiB of pickle in a few KiB of zlib
	if len(bomb) > 64<<10 {
		t.Fatalf("the test bomb is not small: %d bytes", len(bomb))
	}
	if _, err := Decode(bomb); !errors.Is(err, errTooLarge) {
		t.Errorf("want errTooLarge, got %v", err)
	}
}

func TestDecodeLimitsNesting(t *testing.T) {
	var p bytes.Buffer
	p.Write([]byte{opProto, 2})
	for i := 0; i < maxDepth+4; i++ {
		p.Write([]byte{opEmptyList, opMark})
	}
	p.WriteByte(opNone)
	for i := 0; i < maxDepth+4; i++ {
		p.Write([]byte{opAppends})
	}
	p.WriteByte(opStop)
	if _, err := Decode(z(t, p.Bytes())); err == nil {
		t.Error("deeply nested data must be refused")
	}
}

// The expected bytes were checked with Python: pickle.loads(zlib.decompress(...)) of them gives
// {'method': 'get', 'func': 'sso_result', 'sso_token': 'abcdefghijklmnop'}.
func TestEncodeStringsMakesAPickleThatPythonReads(t *testing.T) {
	enc := EncodeStrings(map[string]string{"sso_token": "abcdefghijklmnop", "method": "get", "func": "sso_result"})
	zr, err := zlib.NewReader(bytes.NewReader(enc))
	if err != nil {
		t.Fatalf("not a zlib stream: %v", err)
	}
	inflated, _ := io.ReadAll(zr)
	want := "\x80\x02}(" +
		"X\x04\x00\x00\x00funcX\x0a\x00\x00\x00sso_result" +
		"X\x06\x00\x00\x00methodX\x03\x00\x00\x00get" +
		"X\x09\x00\x00\x00sso_tokenX\x10\x00\x00\x00abcdefghijklmnop" +
		"u."
	if string(inflated) != want {
		t.Errorf("pickle bytes differ:\n got %q\nwant %q", inflated, want)
	}
	back, err := Decode(enc)
	if err != nil || !reflect.DeepEqual(back, map[string]any{"method": "get", "func": "sso_result", "sso_token": "abcdefghijklmnop"}) {
		t.Errorf("round trip: %#v, %v", back, err)
	}
	if !bytes.Equal(enc, EncodeStrings(map[string]string{"method": "get", "func": "sso_result", "sso_token": "abcdefghijklmnop"})) {
		t.Error("the bytes must not depend on the map order")
	}
}

func TestEncodeStringsHandlesNonASCIIAndEmpty(t *testing.T) {
	in := map[string]string{"名": "陈小明", "e": ""}
	back, err := Decode(EncodeStrings(in))
	if err != nil || !reflect.DeepEqual(back, map[string]any{"名": "陈小明", "e": ""}) {
		t.Errorf("%#v, %v", back, err)
	}
	empty, err := Decode(EncodeStrings(nil))
	if err != nil || !reflect.DeepEqual(empty, map[string]any{}) {
		t.Errorf("empty dict: %#v, %v", empty, err)
	}
}

func TestFloat(t *testing.T) {
	if f, ok := Float(1700000123.5); !ok || f != 1700000123.5 {
		t.Error("float")
	}
	if f, ok := Float(int64(5)); !ok || f != 5 {
		t.Error("int")
	}
	for _, v := range []any{"5", nil, true, []any{}} {
		if _, ok := Float(v); ok {
			t.Errorf("%#v is not a number", v)
		}
	}
}
