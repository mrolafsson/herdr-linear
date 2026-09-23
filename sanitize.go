package main

import (
	"reflect"
	"strings"
)

// Everything Linear sends is typed by other people: an issue title can carry
// an escape sequence that rewrites the clipboard (OSC 52), retitles the
// terminal or fakes screen content. So every string from the API is cleaned
// once, at decode time, before any of it can reach the screen.

// multiline names the fields that are prose (Markdown): they keep their line
// breaks and tabs. Every other string is a one-line label.
var multiline = map[string]bool{"Description": true, "Content": true}

// clean removes escape sequences and control characters. keepBreaks keeps
// newlines and tabs (for prose); otherwise they become spaces.
func clean(s string, keepBreaks bool) string {
	if !strings.ContainsFunc(s, isControl) {
		return s
	}
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\n' || r == '\t':
			if keepBreaks {
				b.WriteRune(r)
			} else {
				b.WriteByte(' ')
			}
		case r == '\r':
			// Dropped: a CRLF keeps its \n, a lone CR would move the cursor.
		case r == 0x1b || r == 0x9b || r == 0x9d || r == 0x90 || r == 0x9e || r == 0x9f || r == 0x98:
			i = skipSequence(rs, i)
		case isControl(r):
			// Any other C0/C1 control or DEL: dropped.
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isControl(r rune) bool {
	return r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

// skipSequence returns the index of the last rune of the escape sequence that
// starts at i, so the caller resumes after it. Unterminated sequences run to
// the end of the string: better to lose the tail than to let it through.
func skipSequence(rs []rune, i int) int {
	r := rs[i]
	kind := r
	if r == 0x1b {
		if i+1 >= len(rs) {
			return i
		}
		switch rs[i+1] {
		case '[':
			kind = 0x9b
		case ']':
			kind = 0x9d
		case 'P':
			kind = 0x90
		case 'X', '^', '_':
			kind = 0x98
		default:
			return i + 1 // a two-character escape, e.g. ESC c (reset)
		}
		i++
	}
	switch kind {
	case 0x9b: // CSI: parameters, then one final byte in @–~
		for j := i + 1; j < len(rs); j++ {
			if rs[j] >= 0x40 && rs[j] <= 0x7e {
				return j
			}
		}
	default: // OSC, DCS, SOS, PM, APC: until BEL or ST (ESC \ or 0x9c)
		for j := i + 1; j < len(rs); j++ {
			switch {
			case rs[j] == 0x07 || rs[j] == 0x9c:
				return j
			case rs[j] == 0x1b && j+1 < len(rs) && rs[j+1] == '\\':
				return j + 1
			}
		}
	}
	return len(rs) - 1
}

// screenSafe is the last line of defence, run on everything drawn: it keeps
// only SGR sequences (colour and style: ESC [ … m) and newlines, and drops
// every other escape sequence and control character. Cleaning input isn't
// enough on its own: the Markdown renderer decodes HTML entities, so a
// description can spell an escape as &#27; and have it made real after
// cleaning. Checking the output closes every such path at once.
func screenSafe(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r != '\n' && isControl(r) }) {
		return s
	}
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\n':
			b.WriteRune(r)
		case r == 0x1b && i+1 < len(rs) && rs[i+1] == '[':
			end := skipSequence(rs, i)
			if end < len(rs) && rs[end] == 'm' && sgrParams(rs[i+2:end]) {
				b.WriteString(string(rs[i : end+1]))
			}
			i = end
		case r == 0x1b || r == 0x9b || r == 0x9d || r == 0x90 || r == 0x9e || r == 0x9f || r == 0x98:
			i = skipSequence(rs, i)
		case isControl(r):
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// sgrParams: an SGR's parameters are digits, ';' and ':' only.
func sgrParams(rs []rune) bool {
	for _, r := range rs {
		if (r < '0' || r > '9') && r != ';' && r != ':' {
			return false
		}
	}
	return true
}

// sanitize cleans every string reachable from v (a pointer to decoded API
// data) in place.
func sanitize(v any) {
	sanitizeValue(reflect.ValueOf(v), false)
}

func sanitizeValue(v reflect.Value, keepBreaks bool) {
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			sanitizeValue(v.Elem(), keepBreaks)
		}
	case reflect.Struct:
		t := v.Type()
		for i := 0; i < v.NumField(); i++ {
			sanitizeValue(v.Field(i), multiline[t.Field(i).Name])
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeValue(v.Index(i), keepBreaks)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			e := reflect.New(v.Type().Elem()).Elem()
			e.Set(v.MapIndex(k))
			sanitizeValue(e, keepBreaks)
			v.SetMapIndex(k, e)
		}
	case reflect.String:
		if v.CanSet() {
			v.SetString(clean(v.String(), keepBreaks))
		}
	}
}
