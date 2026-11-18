package ui

import (
	"strings"
	"testing"

	"github.com/rohanthewiz/element"
)

// TestNoteEditorOptsIntoTheUnsavedGuard pins the contract between the editor
// and app.js: the form carries data-guard-unsaved, and a form re-rendered after
// a rejected save says its text is already unsaved. Without the second half, a
// user who was just told "Not saved" could leave the page with no warning,
// because nothing has been typed into that copy of the page yet.
func TestNoteEditorOptsIntoTheUnsavedGuard(t *testing.T) {
	render := func(e NoteEditor) string {
		b := element.NewBuilder()
		e.Render(b)
		return b.String()
	}

	fresh := render(NoteEditor{Action: "/notes", Heading: "New note"})
	if !strings.Contains(fresh, `data-guard-unsaved="1"`) {
		t.Errorf("a fresh editor is not guarded:\n%s", fresh)
	}

	rejected := render(NoteEditor{Action: "/notes", Heading: "New note",
		BodyMD: "an hour of typing", Problem: "Could not read 1 reference"})
	if !strings.Contains(rejected, `data-guard-unsaved="dirty"`) {
		t.Errorf("an editor re-rendered after a rejected save does not start out unsaved:\n%s", rejected)
	}
}
