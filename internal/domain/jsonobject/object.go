// Package jsonobject validates bounded, unambiguous metadata objects.
package jsonobject

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

var ErrInvalidObject = errors.New("invalid JSON object")

// Normalize rejects duplicate object keys at any depth before JSONB would discard them.
// Token walking avoids float conversion and bounds nesting independently of
// the decoder's implementation limit. NUL is rejected for PostgreSQL JSONB.
func Normalize(raw json.RawMessage, maxBytes int) (json.RawMessage, error) {
	if maxBytes < 2 {
		return nil, ErrInvalidObject
	}
	if len(raw) == 0 {
		return json.RawMessage(`{}`), nil
	}
	if len(raw) > maxBytes || !utf8.Valid(raw) || !validSurrogates(raw) {
		return nil, ErrInvalidObject
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, ErrInvalidObject
	}
	if err := consumeContainer(d, '{', 1); err != nil {
		return nil, err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidObject
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, raw); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func consumeContainer(d *json.Decoder, kind json.Delim, depth int) error {
	if depth > 32 {
		return ErrInvalidObject
	}
	seen := map[string]bool{}
	for d.More() {
		if kind == '{' {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] || containsNUL(key) {
				return ErrInvalidObject
			}
			seen[key] = true
		}
		token, err := d.Token()
		if err != nil {
			return ErrInvalidObject
		}
		if value, ok := token.(string); ok && containsNUL(value) {
			return ErrInvalidObject
		}
		if number, ok := token.(json.Number); ok {
			if i := strings.IndexAny(string(number), "eE"); i >= 0 {
				exponent, err := strconv.ParseInt(string(number)[i+1:], 10, 32)
				if err != nil || exponent < -1000 || exponent > 1000 {
					return ErrInvalidObject
				}
			}
		}
		if delimiter, ok := token.(json.Delim); ok {
			if delimiter != '{' && delimiter != '[' {
				return ErrInvalidObject
			}
			if err := consumeContainer(d, delimiter, depth+1); err != nil {
				return err
			}
		}
	}
	end, err := d.Token()
	want := json.Delim('}')
	if kind == '[' {
		want = ']'
	}
	if err != nil || end != want {
		return ErrInvalidObject
	}
	return nil
}

func containsNUL(s string) bool {
	for _, r := range s {
		if r == 0 {
			return true
		}
	}
	return false
}

// encoding/json replaces lone UTF-16 surrogates, whereas PostgreSQL rejects
// them. Reject rather than silently changing configuration text during decode.
func validSurrogates(raw []byte) bool {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) {
			return false
		}
		if raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return false
		}
		i += 4
		if n >= 0xDC00 && n <= 0xDFFF {
			return false
		}
		if n >= 0xD800 && n <= 0xDBFF {
			if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
			if err != nil || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
	}
	return true
}
