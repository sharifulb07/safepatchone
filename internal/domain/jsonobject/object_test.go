package jsonobject_test

import (
	"errors"
	"testing"

	"safepatch/internal/domain/jsonobject"
)

func TestNormalizeObjectOwnershipAndBounds(t *testing.T) {
	input := []byte(` { "large":9007199254740993, "text":"\uD83D\uDE00" } `)
	got, err := jsonobject.Normalize(input, 1024)
	if err != nil {
		t.Fatal(err)
	}
	input[0] = '!'
	if string(got) != `{"large":9007199254740993,"text":"\uD83D\uDE00"}` {
		t.Fatal("normalization changed content or leaked input")
	}
	if got, err := jsonobject.Normalize(nil, 2); err != nil || string(got) != "{}" {
		t.Fatal("empty default")
	}
	if _, err := jsonobject.Normalize(nil, 1); !errors.Is(err, jsonobject.ErrInvalidObject) {
		t.Fatal("invalid bound accepted")
	}
	if _, err := jsonobject.Normalize([]byte(`{"x":1}`), 2); !errors.Is(err, jsonobject.ErrInvalidObject) {
		t.Fatal("size bound ignored")
	}
}
