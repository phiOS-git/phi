package agent

import (
	"strings"
	"testing"
)

func TestTranscriptMirrorAndSearch(t *testing.T) {
	m := testModel(t)
	if _, err := m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := m.NewProject("study", ProjectMeta{}); err != nil {
		t.Fatal(err)
	}

	if err := m.WriteTranscript("study", "20260101-100000", "Group theory",
		"## you\nwhat is a coset\n\n## agent\na coset is ..."); err != nil {
		t.Fatal(err)
	}
	if err := m.WriteTranscript("study", "20260101-110000", "Linear maps",
		"## you\nkernel and image\n\n## agent\nthe kernel is the preimage of zero"); err != nil {
		t.Fatal(err)
	}
	// An unfiled conversation.
	if err := m.WriteTranscript("", "20260101-120000", "Random", "## you\nhello"); err != nil {
		t.Fatal(err)
	}

	list, err := m.ListTranscripts("study")
	if err != nil || len(list) != 2 {
		t.Fatalf("ListTranscripts(study) = %v, err %v", list, err)
	}

	// Pin one; it must sort first and be recorded in project.json.
	if err := m.SetTranscriptPin("20260101-110000", true); err != nil {
		t.Fatal(err)
	}
	list, _ = m.ListTranscripts("study")
	if list[0].ID != "20260101-110000" || !list[0].Pinned {
		t.Errorf("pinned transcript did not sort first: %+v", list)
	}
	meta, _ := m.LoadProjectMeta("study")
	if len(meta.Pins) != 1 || meta.Pins[0] != "20260101-110000" {
		t.Errorf("project.json pins = %v", meta.Pins)
	}

	// Body-only match must find the right conversation, never touching any DB.
	res, err := m.Search("preimage of zero", "")
	if err != nil {
		t.Fatal(err)
	}
	if !hasHit(res, "20260101-110000", false, true) {
		t.Errorf("body search miss: %+v", res)
	}
	// Title match is flagged separately.
	res, _ = m.Search("group theory", "")
	if !hasHit(res, "20260101-100000", true, false) {
		t.Errorf("title search miss: %+v", res)
	}
	// Retitle updates both the front-matter and the heading.
	if err := m.SetTranscriptTitle("20260101-120000", "Renamed chat"); err != nil {
		t.Fatal(err)
	}
	_, md, _ := m.TranscriptByID("20260101-120000")
	if !strings.Contains(md, "title: Renamed chat") || !strings.Contains(md, "# Renamed chat") {
		t.Errorf("retitle incomplete:\n%s", md)
	}
}

func hasHit(res SearchResults, id string, wantTitle, wantBody bool) bool {
	for _, g := range res.Groups {
		for _, h := range g.Hits {
			if h.ID == id && h.InTitle == wantTitle && h.InBody == wantBody {
				return true
			}
		}
	}
	return false
}
