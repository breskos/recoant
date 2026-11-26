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

func TestDelete(t *testing.T) {
	d := New()
	d.Intern("a")
	d.Intern("b")
	if i, ok := d.Delete("a"); !ok || i != 0 {
		t.Fatal("Delete")
	}
	if _, ok := d.Delete("a"); ok {
		t.Fatal("Delete twice")
	}
	if !d.Deleted(0) || d.Deleted(1) || d.Deleted(5) || d.Tombstones() != 1 || d.ID(0) != "" {
		t.Fatal("Deleted")
	}
	if _, ok := d.Lookup("a"); ok {
		t.Fatal("Lookup after delete")
	}
	if d.Intern("a") != 2 || d.Len() != 3 {
		t.Fatal("re-intern must get a new index")
	}
	e, ok := FromIDs([]string{"", "x", ""})
	if !ok || e.Tombstones() != 2 || !e.Deleted(2) {
		t.Fatal("FromIDs with tombstones")
	}
}
