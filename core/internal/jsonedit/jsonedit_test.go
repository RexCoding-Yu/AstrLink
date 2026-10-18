package jsonedit

import (
	"errors"
	"testing"
)

func TestInsertKeepsElementBytes(t *testing.T) {
	const body = " \n{ \"z\":1.00, \"list\" : [ {\"b\":1e+02} ,  \"\\u0061\" ], \"empty\":[  ] }\r\n"
	for _, test := range []struct {
		name  string
		path  string
		index int
		want  string
	}{
		{"prepend", "list", 0, " \n{ \"z\":1.00, \"list\" : [\"x\", {\"b\":1e+02} ,  \"\\u0061\" ], \"empty\":[  ] }\r\n"},
		{"middle", "list", 1, " \n{ \"z\":1.00, \"list\" : [ {\"b\":1e+02} ,  \"x\",\"\\u0061\" ], \"empty\":[  ] }\r\n"},
		{"append", "list", 2, " \n{ \"z\":1.00, \"list\" : [ {\"b\":1e+02} ,  \"\\u0061\",\"x\" ], \"empty\":[  ] }\r\n"},
		{"past end", "list", 9, " \n{ \"z\":1.00, \"list\" : [ {\"b\":1e+02} ,  \"\\u0061\",\"x\" ], \"empty\":[  ] }\r\n"},
		{"empty", "empty", 3, " \n{ \"z\":1.00, \"list\" : [ {\"b\":1e+02} ,  \"\\u0061\" ], \"empty\":[\"x\"  ] }\r\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Insert([]byte(body), test.path, test.index, "x")
			if err != nil || string(got) != test.want {
				t.Fatalf("Insert = %q, %v\nwant %q", got, err, test.want)
			}
		})
	}
	if _, err := Insert([]byte(body), "z", 0, "x"); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("insert into a number = %v", err)
	}
}

func TestSetAndDeleteKeepEnvelope(t *testing.T) {
	const body = " \r\n{ \"z\":1.00, \"a\":null\n }\t\n"
	got, err := Set([]byte(body), "added", "<b>&")
	if err != nil || string(got) != " \r\n{ \"z\":1.00, \"a\":null\n ,\"added\":\"<b>&\"}\t\n" {
		t.Fatalf("Set = %q, %v", got, err)
	}
	got, err = Delete(got, "a")
	if err != nil || string(got) != " \r\n{ \"z\":1.00\n ,\"added\":\"<b>&\"}\t\n" {
		t.Fatalf("Delete = %q, %v", got, err)
	}
	if _, err := SetRaw([]byte("  "), "a", []byte("1")); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("empty document = %v", err)
	}
}
