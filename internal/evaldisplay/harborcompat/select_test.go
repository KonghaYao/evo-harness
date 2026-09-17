package harborcompat

import "testing"

func TestSplitSelectRespectsEmbeds(t *testing.T) {
	got := splitSelect("org_id, organization(id, name, display_name, kind)")
	if len(got) != 2 {
		t.Fatalf("cols=%v", got)
	}
	if got[0] != "org_id" {
		t.Fatalf("got[0]=%q", got[0])
	}
	if got[1] != "organization(id, name, display_name, kind)" {
		t.Fatalf("got[1]=%q", got[1])
	}
}
