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
