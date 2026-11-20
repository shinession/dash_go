// Package pyobj reads and writes the few values the Python dash backend exchanges with dash_go: the result of the
// Windows single sign-on service, which the backend returns as zlib.compress(pickle.dumps(dict)), and the request
// that asks for it.
//
// A pickle is a program for a stack machine that can import modules and call functions, so loading data written by
// another program is remote code execution waiting to happen. pickle.go (taken from the TimeVault project, where it
// reads the user directory in Redis) therefore never executes or instantiates anything: it interprets the opcodes
// into an inert tree. Decode turns that tree into plain Go values and refuses everything that is not plain data.
package pyobj

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
)

// ErrUnsupported means the pickle holds something that is not plain data (an object, a class, a set).
var ErrUnsupported = errors.New("pyobj: the value is not plain data")

// Decode reads zlib.compress(pickle.dumps(v)) and returns v as nil, bool, int64, float64, string, []byte, []any or
// map[string]any. Tuples become []any. Whatever the input, it returns an error instead of panicking.
func Decode(raw []byte) (any, error) {
	v, err := decodeField(raw)
	if err != nil {
		return nil, err
	}
	return plain(v, 0)
}

const maxDepth = 16

func plain(v value, depth int) (any, error) {
	if depth > maxDepth {
		return nil, overLimit("nesting")
	}
	switch x := v.(type) {
	case nil, bool, int64, float64, string, []byte:
		return x, nil
	case *big.Int:
		return nil, fmt.Errorf("%w: integer too large", ErrUnsupported)
	case pyTuple:
		return plainItems([]value(x), depth)
	case *pyList:
		return plainItems(x.items, depth)
	case *pyDict:
		out := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			key, ok := k.(string)
			if !ok {
				return nil, fmt.Errorf("%w: a dict key is not a string", ErrUnsupported)
			}
			val, err := plain(x.vals[i], depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = val
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: %T", ErrUnsupported, v)
}

func plainItems(items []value, depth int) ([]any, error) {
	out := make([]any, 0, len(items))
	for _, it := range items {
		v, err := plain(it, depth+1)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// EncodeStrings returns zlib.compress(pickle.dumps(d)) for a dict of texts, which Python's pickle_loads reads. The
// pickle uses protocol 2 (opcodes every Python 3 understands) and the keys are sorted, so the bytes are reproducible.
func EncodeStrings(d map[string]string) []byte {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var p bytes.Buffer
	p.Write([]byte{opProto, 2, opEmptyDict, opMark})
	text := func(s string) {
		p.WriteByte(opBinUnicode)
		var n [4]byte
		binary.LittleEndian.PutUint32(n[:], uint32(len(s)))
		p.Write(n[:])
		p.WriteString(s)
	}
	for _, k := range keys {
		text(k)
		text(d[k])
	}
	p.WriteByte(opSetItems)
	p.WriteByte(opStop)

	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	_, _ = w.Write(p.Bytes())
	_ = w.Close()
	return z.Bytes()
}

// Float reads a number a Python service stored (time.time() is a float, an integer is accepted too).
func Float(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case int64:
		return float64(x), true
	}
	return 0, false
}
