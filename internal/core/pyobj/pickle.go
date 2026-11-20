package pyobj

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// This file reads the values dash stores in Redis: zlib.compress(pickle.dumps(v)).
//
// A pickle is a program for a stack machine that can import modules and call
// functions, so loading data written by another program is remote code
// execution waiting to happen. The decoder below therefore never executes or
// instantiates anything. It interprets the opcodes into an inert tree of plain
// Go values and records "class X was called with these arguments" as data.
// What the tree means is decided afterwards by interpret.go, which recognises
// a handful of known shapes and rejects everything else.

// Limits that keep a corrupt or hostile value cheap to reject.
const (
	maxInflated = 1 << 20  // bytes of pickle after inflating, and of the compressed input
	maxStack    = 1000     // values on the stack, and open marks
	maxMemo     = 10_000   // memo entries
	maxOps      = 200_000  // opcodes executed
	maxBlob     = 64 << 10 // one string, bytes value, line or big integer
	maxItems    = 10_000   // items of one container
	maxNumText  = 4300     // digits of a text integer, the limit CPython puts on int/str conversion
)

var (
	errInflate    = errors.New("pickle: invalid zlib stream")
	errTooLarge   = errors.New("pickle: inflated data too large")
	errTruncated  = errors.New("pickle: truncated stream")
	errMalformed  = errors.New("pickle: malformed stream")
	errUnderflow  = errors.New("pickle: stack underflow")
	errLimit      = errors.New("pickle: limit exceeded")
	errOpcode     = errors.New("pickle: opcode not allowed")
	errInternal   = errors.New("pickle: internal error")
	errUnexpected = errors.New("unexpected value type")
)

func malformed(what string) error { return fmt.Errorf("%w: %s", errMalformed, what) }
func overLimit(what string) error { return fmt.Errorf("%w: %s", errLimit, what) }

// value is a decoded Python value: nil (None), bool, int64, *big.Int, float64,
// string, []byte, pyTuple, *pyList, *pySet, *pyDict, pyGlobal or *pyObject.
type value = any

type pyTuple []value

type pyList struct{ items []value }

// pySet stands for both set and frozenset.
type pySet struct{ items []value }

// pyDict keeps the pairs in stream order, duplicates included.
type pyDict struct{ keys, vals []value }

// pyGlobal is a reference to a class or function. It is only a name.
type pyGlobal struct{ Module, Name string }

// pyObject records that Class was called with Args (REDUCE, NEWOBJ, NEWOBJ_EX)
// and later received State (BUILD). Nothing was called. Class is empty when the
// callable was not a global but the result of another call.
type pyObject struct {
	Class  pyGlobal
	Args   pyTuple
	Kwargs *pyDict
	State  value
}

// decodeField decodes one hash field: zlib, then pickle. Whatever the input,
// it returns an error instead of panicking.
func decodeField(raw []byte) (v value, err error) {
	defer func() {
		if recover() != nil {
			v, err = nil, errInternal
		}
	}()
	data, err := inflate(raw)
	if err != nil {
		return nil, err
	}
	return parsePickle(data)
}

// inflate decompresses raw. The output limit also stops zip bombs.
func inflate(raw []byte) ([]byte, error) {
	if len(raw) > maxInflated {
		return nil, errTooLarge
	}
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, errInflate
	}
	defer zr.Close()
	out, err := io.ReadAll(io.LimitReader(zr, maxInflated+1))
	if err != nil {
		return nil, errInflate
	}
	if len(out) > maxInflated {
		return nil, errTooLarge
	}
	return out, nil
}

// Pickle opcodes of protocols 0 to 5, named as in CPython's pickle module.
const (
	opMark           = '('
	opStop           = '.'
	opPop            = '0'
	opPopMark        = '1'
	opDup            = '2'
	opFloat          = 'F'
	opBinFloat       = 'G'
	opInt            = 'I'
	opBinInt         = 'J'
	opBinInt1        = 'K'
	opLong           = 'L'
	opBinInt2        = 'M'
	opNone           = 'N'
	opPersID         = 'P'
	opBinPersID      = 'Q'
	opReduce         = 'R'
	opString         = 'S'
	opBinString      = 'T'
	opShortBinString = 'U'
	opUnicode        = 'V'
	opBinUnicode     = 'X'
	opAppend         = 'a'
	opBuild          = 'b'
	opGlobal         = 'c'
	opDict           = 'd'
	opEmptyDict      = '}'
	opAppends        = 'e'
	opGet            = 'g'
	opBinGet         = 'h'
	opInst           = 'i'
	opLongBinGet     = 'j'
	opList           = 'l'
	opEmptyList      = ']'
	opObj            = 'o'
	opPut            = 'p'
	opBinPut         = 'q'
	opLongBinPut     = 'r'
	opSetItem        = 's'
	opTuple          = 't'
	opEmptyTuple     = ')'
	opSetItems       = 'u'
	opBinBytes       = 'B'
	opShortBinBytes  = 'C'

	opProto           = 0x80
	opNewObj          = 0x81
	opExt1            = 0x82
	opExt2            = 0x83
	opExt4            = 0x84
	opTuple1          = 0x85
	opTuple2          = 0x86
	opTuple3          = 0x87
	opNewTrue         = 0x88
	opNewFalse        = 0x89
	opLong1           = 0x8a
	opLong4           = 0x8b
	opShortBinUnicode = 0x8c
	opBinUnicode8     = 0x8d
	opBinBytes8       = 0x8e
	opEmptySet        = 0x8f
	opAddItems        = 0x90
	opFrozenSet       = 0x91
	opNewObjEx        = 0x92
	opStackGlobal     = 0x93
	opMemoize         = 0x94
	opFrame           = 0x95
	opByteArray8      = 0x96
	opNextBuffer      = 0x97
	opReadonlyBuffer  = 0x98
)

type unpickler struct {
	data  []byte
	pos   int
	stack []value
	marks []int // stack heights recorded by MARK; values below the last one are out of reach
	memo  map[int]value
	ops   int
}

// parsePickle runs the pickle program in data and returns the value left by
// STOP. It reads with bounds checks only, so malformed input yields an error.
func parsePickle(data []byte) (value, error) {
	u := &unpickler{data: data, memo: map[int]value{}}
	for {
		if u.ops++; u.ops > maxOps {
			return nil, overLimit("opcodes")
		}
		op, err := u.byte()
		if err != nil {
			return nil, err
		}
		if op == opStop {
			return u.pop()
		}
		if err := u.step(op); err != nil {
			return nil, err
		}
	}
}

func (u *unpickler) take(n int) ([]byte, error) {
	if n < 0 || n > len(u.data)-u.pos {
		return nil, errTruncated
	}
	b := u.data[u.pos : u.pos+n]
	u.pos += n
	return b, nil
}

func (u *unpickler) byte() (byte, error) {
	b, err := u.take(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// uint reads a little-endian unsigned integer of width bytes.
func (u *unpickler) uint(width int) (uint64, error) {
	b, err := u.take(width)
	if err != nil {
		return 0, err
	}
	var n uint64
	for i := width - 1; i >= 0; i-- {
		n = n<<8 | uint64(b[i])
	}
	return n, nil
}

// sized reads a length of width bytes and then that many bytes.
func (u *unpickler) sized(width int) ([]byte, error) {
	n, err := u.uint(width)
	if err != nil {
		return nil, err
	}
	if n > maxBlob {
		return nil, overLimit("string size")
	}
	return u.take(int(n))
}

// line reads up to the next newline, which is consumed but not returned.
func (u *unpickler) line(limit int) ([]byte, error) {
	rest := u.data[u.pos:]
	i := bytes.IndexByte(rest, '\n')
	if i < 0 {
		return nil, errTruncated
	}
	if i > limit {
		return nil, overLimit("line length")
	}
	u.pos += i + 1
	return rest[:i], nil
}

func (u *unpickler) push(v value) error {
	if len(u.stack) >= maxStack {
		return overLimit("stack depth")
	}
	u.stack = append(u.stack, v)
	return nil
}

// fence is the stack height of the innermost open mark.
func (u *unpickler) fence() int {
	if n := len(u.marks); n > 0 {
		return u.marks[n-1]
	}
	return 0
}

func (u *unpickler) top() (value, error) {
	if len(u.stack) <= u.fence() {
		return nil, errUnderflow
	}
	return u.stack[len(u.stack)-1], nil
}

func (u *unpickler) pop() (value, error) {
	v, err := u.top()
	if err != nil {
		return nil, err
	}
	u.stack = u.stack[:len(u.stack)-1]
	return v, nil
}

func (u *unpickler) popN(n int) ([]value, error) {
	if len(u.stack)-u.fence() < n {
		return nil, errUnderflow
	}
	items := append([]value(nil), u.stack[len(u.stack)-n:]...)
	u.stack = u.stack[:len(u.stack)-n]
	return items, nil
}

// popMark removes the innermost mark and returns the values above it.
func (u *unpickler) popMark() ([]value, error) {
	if len(u.marks) == 0 {
		return nil, malformed("no mark")
	}
	m := u.marks[len(u.marks)-1]
	u.marks = u.marks[:len(u.marks)-1]
	items := make([]value, len(u.stack)-m)
	copy(items, u.stack[m:])
	u.stack = u.stack[:m]
	return items, nil
}

// popItems pops what APPEND (one value), SETITEM (key and value) or the
// "many" opcodes APPENDS and SETITEMS (everything above the mark) consume.
func (u *unpickler) popItems(op byte) ([]value, error) {
	switch op {
	case opAppend:
		return u.popN(1)
	case opSetItem:
		return u.popN(2)
	default:
		return u.popMark()
	}
}

// width is the size in bytes of the number an opcode carries: a value, an index or a length.
func width(op byte) int {
	switch op {
	case opBinInt1, opLong1, opShortBinString, opShortBinUnicode, opShortBinBytes, opBinPut, opBinGet:
		return 1
	case opBinInt2:
		return 2
	case opBinInt, opLong4, opBinString, opBinUnicode, opBinBytes, opLongBinPut, opLongBinGet:
		return 4
	default: // BINUNICODE8, BINBYTES8 and BYTEARRAY8
		return 8
	}
}

func topAs[T any](u *unpickler, what string) (T, error) {
	var zero T
	v, err := u.top()
	if err != nil {
		return zero, err
	}
	t, ok := v.(T)
	if !ok {
		return zero, malformed(what)
	}
	return t, nil
}

func popAs[T any](u *unpickler, what string) (T, error) {
	t, err := topAs[T](u, what)
	if err != nil {
		return t, err
	}
	u.stack = u.stack[:len(u.stack)-1]
	return t, nil
}

func (u *unpickler) memoPut(idx int) error {
	v, err := u.top()
	if err != nil {
		return err
	}
	if _, ok := u.memo[idx]; !ok && len(u.memo) >= maxMemo {
		return overLimit("memo entries")
	}
	u.memo[idx] = v
	return nil
}

func (u *unpickler) memoGet(idx int) error {
	v, ok := u.memo[idx]
	if !ok {
		return malformed("unknown memo entry")
	}
	return u.push(v)
}

// textIndex reads the decimal memo index of PUT and GET.
func (u *unpickler) textIndex() (int, error) {
	line, err := u.line(maxNumText)
	if err != nil {
		return 0, err
	}
	idx, err := strconv.Atoi(string(line))
	if err != nil || idx < 0 {
		return 0, malformed("memo index")
	}
	return idx, nil
}

func (u *unpickler) step(op byte) error {
	switch op {
	case opProto:
		v, err := u.byte()
		if err != nil {
			return err
		}
		if v > 5 {
			return malformed("unsupported protocol")
		}
		return nil
	case opFrame:
		_, err := u.take(8) // the frame length is only a hint
		return err

	case opNone:
		return u.push(nil)
	case opNewTrue:
		return u.push(true)
	case opNewFalse:
		return u.push(false)

	case opBinInt1, opBinInt2, opBinInt:
		n, err := u.uint(width(op))
		if err != nil {
			return err
		}
		if op == opBinInt {
			return u.push(int64(int32(n)))
		}
		return u.push(int64(n))
	case opInt, opLong:
		line, err := u.line(maxNumText)
		if err != nil {
			return err
		}
		return u.pushTextInt(string(line), op == opInt)
	case opLong1, opLong4:
		b, err := u.sized(width(op))
		if err != nil {
			return err
		}
		return u.push(twosComplement(b))
	case opFloat:
		line, err := u.line(maxNumText)
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(string(line)), 64)
		if err != nil {
			return malformed("float")
		}
		return u.push(f)
	case opBinFloat:
		b, err := u.take(8)
		if err != nil {
			return err
		}
		return u.push(math.Float64frombits(binary.BigEndian.Uint64(b)))

	case opString:
		line, err := u.line(maxBlob)
		if err != nil {
			return err
		}
		s, err := unquote(line)
		if err != nil {
			return err
		}
		return u.push(s)
	case opBinString, opShortBinString:
		b, err := u.sized(width(op))
		if err != nil {
			return err
		}
		if !isASCII(b) {
			return malformed("non-ASCII 8-bit string")
		}
		return u.push(string(b))
	case opUnicode:
		line, err := u.line(maxBlob)
		if err != nil {
			return err
		}
		s, err := rawUnicodeEscape(line)
		if err != nil {
			return err
		}
		return u.push(s)
	case opShortBinUnicode, opBinUnicode, opBinUnicode8:
		b, err := u.sized(width(op))
		if err != nil {
			return err
		}
		if !utf8.Valid(b) {
			return malformed("invalid UTF-8")
		}
		return u.push(string(b))
	case opShortBinBytes, opBinBytes, opBinBytes8, opByteArray8:
		b, err := u.sized(width(op))
		if err != nil {
			return err
		}
		return u.push(append([]byte(nil), b...))

	case opEmptyTuple:
		return u.push(pyTuple{})
	case opTuple1, opTuple2, opTuple3:
		items, err := u.popN(int(op-opTuple1) + 1)
		if err != nil {
			return err
		}
		return u.push(pyTuple(items))
	case opTuple:
		items, err := u.popMark()
		if err != nil {
			return err
		}
		return u.push(pyTuple(items))
	case opEmptyList:
		return u.push(&pyList{})
	case opList:
		items, err := u.popMark()
		if err != nil {
			return err
		}
		return u.push(&pyList{items})
	case opAppend, opAppends:
		items, err := u.popItems(op)
		if err != nil {
			return err
		}
		l, err := topAs[*pyList](u, "append target is not a list")
		if err != nil {
			return err
		}
		if len(l.items)+len(items) > maxItems {
			return overLimit("list size")
		}
		l.items = append(l.items, items...)
		return nil
	case opEmptyDict:
		return u.push(&pyDict{})
	case opDict:
		items, err := u.popMark()
		if err != nil {
			return err
		}
		d := &pyDict{}
		if err := d.add(items); err != nil {
			return err
		}
		return u.push(d)
	case opSetItem, opSetItems:
		items, err := u.popItems(op)
		if err != nil {
			return err
		}
		d, err := topAs[*pyDict](u, "setitem target is not a dict")
		if err != nil {
			return err
		}
		return d.add(items)
	case opEmptySet:
		return u.push(&pySet{})
	case opFrozenSet:
		items, err := u.popMark()
		if err != nil {
			return err
		}
		return u.push(&pySet{items})
	case opAddItems:
		items, err := u.popMark()
		if err != nil {
			return err
		}
		s, err := topAs[*pySet](u, "additems target is not a set")
		if err != nil {
			return err
		}
		if len(s.items)+len(items) > maxItems {
			return overLimit("set size")
		}
		s.items = append(s.items, items...)
		return nil

	case opGlobal:
		module, err := u.line(maxBlob)
		if err != nil {
			return err
		}
		name, err := u.line(maxBlob)
		if err != nil {
			return err
		}
		if !utf8.Valid(module) || !utf8.Valid(name) {
			return malformed("invalid UTF-8")
		}
		return u.push(pyGlobal{string(module), string(name)})
	case opStackGlobal:
		name, err := popAs[string](u, "global name is not a string")
		if err != nil {
			return err
		}
		module, err := popAs[string](u, "global module is not a string")
		if err != nil {
			return err
		}
		return u.push(pyGlobal{module, name})
	case opReduce:
		args, err := popAs[pyTuple](u, "arguments are not a tuple")
		if err != nil {
			return err
		}
		callable, err := u.pop()
		if err != nil {
			return err
		}
		switch c := callable.(type) {
		case pyGlobal:
			return u.push(&pyObject{Class: c, Args: args})
		case *pyObject:
			// The result of an earlier call, such as getattr(cls, "method") for a bound
			// method. The class of what it returns stays unknown.
			return u.push(&pyObject{Args: args})
		}
		return malformed("callable is not a global")
	case opNewObj:
		args, err := popAs[pyTuple](u, "arguments are not a tuple")
		if err != nil {
			return err
		}
		class, err := popAs[pyGlobal](u, "class is not a global")
		if err != nil {
			return err
		}
		return u.push(&pyObject{Class: class, Args: args})
	case opNewObjEx:
		kwargs, err := popAs[*pyDict](u, "keyword arguments are not a dict")
		if err != nil {
			return err
		}
		args, err := popAs[pyTuple](u, "arguments are not a tuple")
		if err != nil {
			return err
		}
		class, err := popAs[pyGlobal](u, "class is not a global")
		if err != nil {
			return err
		}
		return u.push(&pyObject{Class: class, Args: args, Kwargs: kwargs})
	case opBuild:
		state, err := u.pop()
		if err != nil {
			return err
		}
		o, err := topAs[*pyObject](u, "build target is not an object")
		if err != nil {
			return err
		}
		o.State = state
		return nil

	case opPut:
		idx, err := u.textIndex()
		if err != nil {
			return err
		}
		return u.memoPut(idx)
	case opBinPut, opLongBinPut:
		idx, err := u.uint(width(op))
		if err != nil {
			return err
		}
		return u.memoPut(int(idx))
	case opMemoize:
		return u.memoPut(len(u.memo))
	case opGet:
		idx, err := u.textIndex()
		if err != nil {
			return err
		}
		return u.memoGet(idx)
	case opBinGet, opLongBinGet:
		idx, err := u.uint(width(op))
		if err != nil {
			return err
		}
		return u.memoGet(int(idx))

	case opMark:
		if len(u.marks) >= maxStack {
			return overLimit("open marks")
		}
		u.marks = append(u.marks, len(u.stack))
		return nil
	case opPop:
		if len(u.marks) > 0 && len(u.stack) == u.fence() {
			u.marks = u.marks[:len(u.marks)-1] // nothing above the mark: the mark is what gets popped
			return nil
		}
		_, err := u.pop()
		return err
	case opPopMark:
		_, err := u.popMark()
		return err
	case opDup:
		v, err := u.top()
		if err != nil {
			return err
		}
		return u.push(v)

	case opInst, opObj, opPersID, opBinPersID, opExt1, opExt2, opExt4, opNextBuffer, opReadonlyBuffer:
		// They look classes up by name, resolve external references or need out-of-band data.
		return fmt.Errorf("%w: 0x%02x", errOpcode, op)
	default:
		return fmt.Errorf("%w: unknown 0x%02x", errOpcode, op)
	}
}

func (d *pyDict) add(items []value) error {
	if len(items)%2 != 0 {
		return malformed("odd number of dict items")
	}
	if len(d.keys)+len(items)/2 > maxItems {
		return overLimit("dict size")
	}
	for i := 0; i < len(items); i += 2 {
		d.keys = append(d.keys, items[i])
		d.vals = append(d.vals, items[i+1])
	}
	return nil
}

// pushTextInt handles INT and LONG, whose argument is decimal text. "I00" and
// "I01" are the protocol 0 spelling of False and True.
func (u *unpickler) pushTextInt(s string, allowBool bool) error {
	s = strings.TrimSpace(s)
	if allowBool && s == "00" {
		return u.push(false)
	}
	if allowBool && s == "01" {
		return u.push(true)
	}
	if !allowBool {
		s = strings.TrimSuffix(s, "L")
	}
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return malformed("integer")
	}
	return u.push(normalizeInt(n))
}

func normalizeInt(n *big.Int) value {
	if n.IsInt64() {
		return n.Int64()
	}
	return n
}

// twosComplement converts the little-endian two's complement bytes of LONG1 and LONG4.
func twosComplement(b []byte) value {
	if len(b) == 0 {
		return int64(0)
	}
	be := make([]byte, len(b))
	for i, c := range b {
		be[len(b)-1-i] = c
	}
	n := new(big.Int).SetBytes(be)
	if b[len(b)-1]&0x80 != 0 {
		n.Sub(n, new(big.Int).Lsh(big.NewInt(1), uint(8*len(b))))
	}
	return normalizeInt(n)
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// unquote reads the argument of STRING, the repr of a Python 2 style str.
// Python 3 decodes it as ASCII, and so does this.
func unquote(line []byte) (string, error) {
	const escapes, plain = `\'"abfnrtv`, "\\'\"\a\b\f\n\r\t\v"
	if len(line) < 2 || line[0] != line[len(line)-1] || (line[0] != '\'' && line[0] != '"') {
		return "", malformed("quoted string")
	}
	b := line[1 : len(line)-1]
	out := make([]byte, 0, len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\\' {
			out = append(out, b[i])
			continue
		}
		if i++; i >= len(b) {
			return "", malformed("quoted string")
		}
		switch c := b[i]; {
		case c == 'x':
			if i+2 >= len(b) {
				return "", malformed("quoted string")
			}
			n, err := strconv.ParseUint(string(b[i+1:i+3]), 16, 8)
			if err != nil {
				return "", malformed("quoted string")
			}
			out = append(out, byte(n))
			i += 2
		case c >= '0' && c <= '7':
			n := 0
			for j := 0; j < 3 && i < len(b) && b[i] >= '0' && b[i] <= '7'; j++ {
				n = n*8 + int(b[i]-'0')
				i++
			}
			out = append(out, byte(n))
			i--
		case strings.IndexByte(escapes, c) >= 0:
			out = append(out, plain[strings.IndexByte(escapes, c)])
		default:
			out = append(out, '\\', c)
		}
	}
	if !isASCII(out) {
		return "", malformed("non-ASCII 8-bit string")
	}
	return string(out), nil
}

// rawUnicodeEscape decodes the argument of UNICODE: bytes are Latin-1 code
// points except for \uXXXX and \UXXXXXXXX escapes.
func rawUnicodeEscape(b []byte) (string, error) {
	var sb strings.Builder
	for i := 0; i < len(b); {
		if b[i] == '\\' && i+1 < len(b) && (b[i+1] == 'u' || b[i+1] == 'U') {
			digits := 4
			if b[i+1] == 'U' {
				digits = 8
			}
			if i+2+digits > len(b) {
				return "", malformed("unicode escape")
			}
			r, err := strconv.ParseUint(string(b[i+2:i+2+digits]), 16, 32)
			if err != nil || r > unicode.MaxRune || (r >= 0xD800 && r < 0xE000) {
				return "", malformed("unicode escape")
			}
			sb.WriteRune(rune(r))
			i += 2 + digits
			continue
		}
		sb.WriteRune(rune(b[i]))
		i++
	}
	return sb.String(), nil
}
