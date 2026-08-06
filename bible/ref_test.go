package bible

import "testing"

func TestParseRef(t *testing.T) {
	tests := []struct {
		in      string
		book    int
		chapter int
		vStart  int
		vEnd    int
	}{
		// Plain forms.
		{"John 3", 43, 3, 0, 0},
		{"John 3:16", 43, 3, 16, 16},
		{"John 3:16-18", 43, 3, 16, 18},

		// A bare book opens at chapter 1.
		{"Genesis", 1, 1, 0, 0},
		{"Revelation", 66, 1, 0, 0},

		// Alternate chapter:verse separators.
		{"John 3.16", 43, 3, 16, 16},
		{"John 3v16", 43, 3, 16, 16},

		// Abbreviations that END in 'v'. The 'v' separator above is only a
		// separator after a digit; otherwise "Lev 16:14" splits as "Le" plus
		// a locator of "v 16:14" and does not parse at all.
		{"Lev 16:14", 3, 16, 14, 14},
		{"Rev 22:1", 66, 22, 1, 1},
		{"REV 22:1", 66, 22, 1, 1},

		// Abbreviations, OSIS codes, BLB slugs and aliases all resolve.
		{"Jn 3:16", 43, 3, 16, 16},
		{"jhn 3:16", 43, 3, 16, 16},
		{"Ps 23", 19, 23, 0, 0},
		{"Psalm 23:1", 19, 23, 1, 1},
		{"Song of Songs 2:1", 22, 2, 1, 1},
		{"Sng 2:1", 22, 2, 1, 1},

		// Numbered books: spaced, unspaced, ordinal and roman prefixes.
		{"1 John 2:1", 62, 2, 1, 1},
		{"1John 2:1", 62, 2, 1, 1},
		{"1st John 2:1", 62, 2, 1, 1},
		{"I John 2:1", 62, 2, 1, 1},
		{"II Corinthians 5:17", 47, 5, 17, 17},
		{"2nd Cor 5:17", 47, 5, 17, 17},

		// Trailing period on the abbreviation.
		{"Gen. 1:1", 1, 1, 1, 1},

		// Case and surrounding whitespace are irrelevant.
		{"  rOmAnS 8:28  ", 45, 8, 28, 28},

		// Single-chapter books cite by verse: "Jude 3" is verse 3, not
		// chapter 3 (Jude has only one chapter).
		{"Jude 3", 65, 1, 3, 3},
		{"Obadiah 15", 31, 1, 15, 15},
		{"Philemon 6", 57, 1, 6, 6},
		{"3 John 4", 64, 1, 4, 4},
		// ...but an explicit chapter:verse still works for them.
		{"Jude 1:3", 65, 1, 3, 3},

		// A backwards range is treated as a typo and normalized.
		{"John 3:18-16", 43, 3, 16, 18},
	}

	for _, tc := range tests {
		got, err := ParseRef(tc.in)
		if err != nil {
			t.Errorf("ParseRef(%q) unexpected error: %v", tc.in, err)
			continue
		}
		if got.BookNum != tc.book || got.Chapter != tc.chapter ||
			got.VerseStart != tc.vStart || got.VerseEnd != tc.vEnd {
			t.Errorf("ParseRef(%q) = {book:%d ch:%d v:%d-%d}, want {book:%d ch:%d v:%d-%d}",
				tc.in, got.BookNum, got.Chapter, got.VerseStart, got.VerseEnd,
				tc.book, tc.chapter, tc.vStart, tc.vEnd)
		}
	}
}

// TestParseRefRejects guards the search page's fast-path: anything that is not
// unambiguously a reference must fail here so the query falls through to a
// text search instead of teleporting the user somewhere unexpected.
func TestParseRefRejects(t *testing.T) {
	for _, in := range []string{
		"",
		"love",
		"the grace of God",
		"Nicodemus",
		"Hezekiah 3:1", // not a book
		"John 99",      // beyond the end of the book
		"John 0",       // chapters are 1-based
		"3:16",         // no book
	} {
		if ref, err := ParseRef(in); err == nil {
			t.Errorf("ParseRef(%q) = %v, want error", in, ref)
		}
	}
}

func TestRefString(t *testing.T) {
	tests := []struct {
		ref  Ref
		want string
	}{
		{Ref{BookNum: 43, Chapter: 3}, "John 3"},
		{Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 16}, "John 3:16"},
		{Ref{BookNum: 43, Chapter: 3, VerseStart: 16, VerseEnd: 18}, "John 3:16-18"},
		{Ref{BookNum: 62, Chapter: 2, VerseStart: 1, VerseEnd: 1}, "1 John 2:1"},
	}
	for _, tc := range tests {
		if got := tc.ref.String(); got != tc.want {
			t.Errorf("Ref%+v.String() = %q, want %q", tc.ref, got, tc.want)
		}
	}
}

// TestRoundTrip is the property that matters for links: every reference the
// app renders must parse back to the same location.
func TestRoundTrip(t *testing.T) {
	for i := range Books {
		bk := &Books[i]
		ref := Ref{BookNum: bk.Num, Chapter: 1, VerseStart: 1, VerseEnd: 1}
		got, err := ParseRef(ref.String())
		if err != nil {
			t.Errorf("%s: rendered %q does not parse: %v", bk.Name, ref.String(), err)
			continue
		}
		if got != ref {
			t.Errorf("%s: %q round-tripped to %+v, want %+v", bk.Name, ref.String(), got, ref)
		}
	}
}

// TestBookLookup checks that every registered spelling resolves to its book,
// and that no two books collide on an alias.
func TestBookLookup(t *testing.T) {
	for i := range Books {
		bk := &Books[i]
		keys := append([]string{bk.Name, bk.OSIS, bk.BLBAbbrev}, bk.Aliases...)
		for _, k := range keys {
			got, err := ByName(k)
			if err != nil {
				t.Errorf("ByName(%q) failed for %s: %v", k, bk.Name, err)
				continue
			}
			if got.Num != bk.Num {
				t.Errorf("ByName(%q) = %s, want %s", k, got.Name, bk.Name)
			}
		}
	}
}

// TestChapterCounts is a sanity check on the canon table: the Protestant canon
// has 1,189 chapters. A typo in any single count breaks the total.
func TestChapterCounts(t *testing.T) {
	total := 0
	for i := range Books {
		total += Books[i].ChapterCount
	}
	if total != 1189 {
		t.Errorf("chapter total = %d, want 1189", total)
	}
	if len(Books) != 66 {
		t.Errorf("book count = %d, want 66", len(Books))
	}
}

// TestParseRefList covers the note editor's References field, where a user
// types several anchors at once and one typo must not cost them the others.
func TestParseRefList(t *testing.T) {
	cases := []struct {
		name         string
		in           string
		wantRefs     []string
		wantRejected []string
	}{
		{
			name:     "semicolon separated",
			in:       "John 3:16-18; Rom 5:8; Ps 23",
			wantRefs: []string{"John 3:16-18", "Romans 5:8", "Psalms 23"},
		},
		{
			name:     "comma separated",
			in:       "John 3:16, Genesis 1:1",
			wantRefs: []string{"John 3:16", "Genesis 1:1"},
		},
		{
			// Semicolons win when both appear, so the second segment stays
			// whole. It is then rejected rather than silently truncated to
			// "Genesis 1:1" — the multi-verse comma form is not a shape
			// ParseRef accepts, and quietly dropping the ", 2" would store an
			// anchor the user did not ask for.
			name:         "semicolons take precedence over commas",
			in:           "John 3:16; Genesis 1:1, 2",
			wantRefs:     []string{"John 3:16"},
			wantRejected: []string{"Genesis 1:1, 2"},
		},
		{
			name:         "partial success reports the rest",
			in:           "John 3:16; not a reference; Rom 5:8",
			wantRefs:     []string{"John 3:16", "Romans 5:8"},
			wantRejected: []string{"not a reference"},
		},
		{
			name: "blank yields nothing at all",
			in:   "   ",
		},
		{
			name:     "empty segments are skipped, not rejected",
			in:       "John 3:16;;  ; Rom 5:8",
			wantRefs: []string{"John 3:16", "Romans 5:8"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			refs, rejected := ParseRefList(c.in)

			if len(refs) != len(c.wantRefs) {
				t.Fatalf("ParseRefList(%q) parsed %d refs, want %d", c.in, len(refs), len(c.wantRefs))
			}
			for i, want := range c.wantRefs {
				if got := refs[i].String(); got != want {
					t.Errorf("ParseRefList(%q)[%d] = %q, want %q", c.in, i, got, want)
				}
			}

			if len(rejected) != len(c.wantRejected) {
				t.Fatalf("ParseRefList(%q) rejected %v, want %v", c.in, rejected, c.wantRejected)
			}
			for i, want := range c.wantRejected {
				if rejected[i] != want {
					t.Errorf("ParseRefList(%q) rejected[%d] = %q, want %q", c.in, i, rejected[i], want)
				}
			}
		})
	}
}

// TestRefListRoundTrip checks that what the editor stores can be re-rendered
// into what the editor accepts — the property that makes editing a note's
// anchors non-destructive.
func TestRefListRoundTrip(t *testing.T) {
	const in = "John 3:16-18; Romans 5:8; Psalms 23"

	refs, rejected := ParseRefList(in)
	if len(rejected) > 0 {
		t.Fatalf("ParseRefList(%q) rejected %v", in, rejected)
	}

	if got := FormatRefList(refs); got != in {
		t.Errorf("FormatRefList(ParseRefList(%q)) = %q, want the input back", in, got)
	}
}
