package library

import "testing"

func TestIndexProgressFirstMatch(t *testing.T) {
	progress := []*Progress{{TitleID: "other", Episode: 7, Completed: true}, {TitleID: "title", Episode: 3, PositionSec: 60}, {TitleID: "title", Episode: 3, Completed: true}}
	index := IndexProgress("title", progress)
	if len(index) != 1 || index[3].PositionSec != 60 || index[3].Completed {
		t.Fatalf("index = %#v", index)
	}
	if index[3] != progress[1] {
		t.Fatal("index should reference the first existing record without copying it")
	}
	if got := IndexProgress("", progress); len(got) != 0 {
		t.Fatalf("missing title index = %#v", got)
	}
}
