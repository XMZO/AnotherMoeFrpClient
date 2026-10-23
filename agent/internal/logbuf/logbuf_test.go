package logbuf

import (
	"io"
	"strings"
	"testing"
)

func TestBuffer(t *testing.T) {
	b := New(3, io.Discard)
	b.Write([]byte("a\nb\r\nc"))
	if got := strings.Join(b.Tail(10), ","); got != "a,b" {
		t.Fatalf("不完整的行先不算：%s", got)
	}
	b.Write([]byte("\nd\ne\n"))
	if got := strings.Join(b.Tail(10), ","); got != "c,d,e" {
		t.Fatalf("环形覆盖：%s", got)
	}
	if got := strings.Join(b.Tail(2), ","); got != "d,e" {
		t.Fatalf("tail 2：%s", got)
	}
}
