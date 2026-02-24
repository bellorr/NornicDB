package search

import "testing"

func TestResolveCompressedANNProfile_InactiveWithoutVectorStore(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_ANN_QUALITY", "compressed")
	p := ResolveCompressedANNProfile(10000, 384, false)
	if p.Active {
		t.Fatalf("expected inactive profile")
	}
	if len(p.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics")
	}
}

func TestResolveCompressedANNProfile_ActiveWithValidSettings(t *testing.T) {
	t.Setenv("NORNICDB_VECTOR_ANN_QUALITY", "compressed")
	t.Setenv("NORNICDB_VECTOR_IVF_LISTS", "256")
	t.Setenv("NORNICDB_VECTOR_PQ_SEGMENTS", "16")
	t.Setenv("NORNICDB_VECTOR_PQ_BITS", "8")
	p := ResolveCompressedANNProfile(50000, 384, true)
	if !p.Active {
		t.Fatalf("expected active profile, diagnostics=%v", p.Diagnostics)
	}
	if p.IVFLists != 256 || p.PQSegments != 16 || p.PQBits != 8 {
		t.Fatalf("unexpected profile values: %+v", p)
	}
}
