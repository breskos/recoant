package dict

import "testing"

func TestDictionary(t *testing.T) {
	d := New()
	if d.Intern("a") != 0 || d.Intern("b") != 1 || d.Intern("a") != 0 {
		t.Fatal("Intern")
	}
	if i, ok := d.Lookup("b"); !ok || i != 1 {
		t.Fatal("Lookup")
	}
	if _, ok := d.Lookup("c"); ok {
		t.Fatal("Lookup missing")
	}
	if d.ID(1) != "b" || d.Len() != 2 || len(d.IDs()) != 2 {
		t.Fatal("ID/Len")
	}
	e, ok := FromIDs([]string{"x", "y"})
	if !ok || e.Intern("y") != 1 || e.Intern("z") != 2 {
		t.Fatal("FromIDs")
	}
	if _, ok := FromIDs([]string{"x", "x"}); ok {
		t.Fatal("duplicate accepted")
	}
}
