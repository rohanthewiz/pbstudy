package bible

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/rohanthewiz/serr"
)

// Ref is a parsed scripture reference: a book, a chapter, and optionally a
// verse or verse range within it.
//
// VerseStart == 0 means "the whole chapter". When a single verse is meant,
// VerseStart == VerseEnd. The zero-vs-range distinction lets one type serve
// both the reader (chapter view) and note anchors (range view) without a
// separate "kind" field.
type Ref struct {
	BookNum    int
	Chapter    int
	VerseStart int
	VerseEnd   int
}

// IsChapter reports whether the reference names a whole chapter.
func (r Ref) IsChapter() bool { return r.VerseStart == 0 }

// IsRange reports whether the reference spans more than one verse.
func (r Ref) IsRange() bool { return r.VerseStart != 0 && r.VerseEnd > r.VerseStart }

// Book returns the referenced book.
func (r Ref) Book() *Book { return MustByNum(r.BookNum) }

// String renders the reference in conventional form: "John 3", "John 3:16",
// "John 3:16-18".
func (r Ref) String() string {
	s := r.Book().Name + " " + strconv.Itoa(r.Chapter)
	if r.VerseStart == 0 {
		return s
	}
	s += ":" + strconv.Itoa(r.VerseStart)
	if r.VerseEnd > r.VerseStart {
		s += "-" + strconv.Itoa(r.VerseEnd)
	}
	return s
}

// ParseRef parses a human-written scripture reference.
//
// Accepted shapes (book name in any spelling ByName accepts):
//
//	John 3          whole chapter
//	John 3:16       single verse
//	John 3:16-18    verse range
//	John 3.16       '.' and 'v' also separate chapter from verse
//	1 John 2:1      numbered books, with or without the space
//	Jude 3          single-chapter book: the number is the VERSE
//
// The parse is deliberately strict about the numeric tail — a query the user
// meant as a text search ("love") must fail here so the search page can fall
// through to a full-text scan rather than guessing.
func ParseRef(s string) (Ref, error) {
	var ref Ref

	s = strings.TrimSpace(s)
	if s == "" {
		return ref, serr.New("empty reference")
	}

	// Split the trailing "chapter[:verse[-verse]]" from the book name. Walk
	// backwards over the numeric tail: everything from the last run of
	// digits-and-separators onward is the locator, the rest is the book.
	// Scanning backwards is what makes numbered books work — "1 John 2:1"
	// has digits at both ends, and only the tail is a locator.
	idx := len(s)
	for idx > 0 {
		r := rune(s[idx-1])
		if unicode.IsDigit(r) || r == ':' || r == '-' || r == '.' || r == ' ' ||
			r == 'v' || r == 'V' {
			idx--
			continue
		}
		break
	}
	bookPart := strings.TrimSpace(s[:idx])
	// The backwards scan accepts separators, so an abbreviation's trailing
	// period ("Gen. 1:1") lands at the head of the locator. Strip any leading
	// separators; the book part keeps its period, which normalizeBookKey
	// discards anyway.
	locPart := strings.TrimLeft(s[idx:], " .:-")

	if bookPart == "" {
		return ref, serr.New("reference has no book name", "ref", s)
	}

	bk, err := ByName(bookPart)
	if err != nil {
		return ref, serr.Wrap(err, "cannot parse reference", "ref", s)
	}
	ref.BookNum = bk.Num

	// A bare book name means chapter 1 — "Genesis" opens at the beginning.
	if locPart == "" {
		ref.Chapter = 1
		return ref, nil
	}

	// Normalize the separators the locator may use: "3.16" and "3v16" both
	// mean "3:16".
	locPart = strings.NewReplacer(".", ":", "v", ":", "V", ":", " ", "").Replace(locPart)

	chapStr, verseStr, hasVerse := strings.Cut(locPart, ":")

	chap, err := strconv.Atoi(chapStr)
	if err != nil || chap < 1 {
		return ref, serr.New("invalid chapter in reference", "ref", s, "chapter", chapStr)
	}

	// Single-chapter books (Obadiah, Philemon, 2/3 John, Jude) are cited by
	// verse alone: "Jude 3" universally means verse 3, not chapter 3. Honour
	// that convention rather than producing a chapter that cannot exist.
	if bk.ChapterCount == 1 && !hasVerse {
		ref.Chapter = 1
		ref.VerseStart, ref.VerseEnd = chap, chap
		return ref, nil
	}

	if chap > bk.ChapterCount {
		return ref, serr.New("chapter beyond end of book", "ref", s,
			"book", bk.Name, "chapter", strconv.Itoa(chap),
			"chapters", strconv.Itoa(bk.ChapterCount))
	}
	ref.Chapter = chap

	if !hasVerse || verseStr == "" {
		return ref, nil // whole chapter
	}

	startStr, endStr, hasRange := strings.Cut(verseStr, "-")
	start, err := strconv.Atoi(startStr)
	if err != nil || start < 1 {
		return ref, serr.New("invalid verse in reference", "ref", s, "verse", startStr)
	}
	ref.VerseStart, ref.VerseEnd = start, start

	if hasRange && endStr != "" {
		end, err := strconv.Atoi(endStr)
		if err != nil || end < 1 {
			return ref, serr.New("invalid verse range in reference", "ref", s, "range", verseStr)
		}
		// A backwards range ("16-14") is more likely a typo than intent;
		// swapping is friendlier than erroring on a study tool.
		if end < start {
			start, end = end, start
			ref.VerseStart = start
		}
		ref.VerseEnd = end
	}

	return ref, nil
}

// LooksLikeRef reports whether s parses as a scripture reference. The search
// page uses this to decide between jumping to a verse and running a text
// search.
func LooksLikeRef(s string) (Ref, bool) {
	ref, err := ParseRef(s)
	return ref, err == nil
}

// ParseRefList parses a semicolon- or comma-separated list of references, as
// typed into the note editor's "References" field:
//
//	John 3:16-18; Rom 5:8; Ps 23
//
// It returns the references that parsed and the raw text of the ones that did
// not, in the order they were written. Partial success is the point: a note
// with three good anchors and one typo should save the three and tell the user
// about the one, not refuse the whole save and make them retype the lot.
//
// Semicolons are tried first because commas appear inside references
// themselves in some citation styles; a list separated by either works, but a
// list separated by both is read as semicolon-delimited.
func ParseRefList(s string) (refs []Ref, rejected []string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}

	sep := ","
	if strings.Contains(s, ";") {
		sep = ";"
	}

	for _, part := range strings.Split(s, sep) {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		ref, err := ParseRef(part)
		if err != nil {
			rejected = append(rejected, part)
			continue
		}
		refs = append(refs, ref)
	}
	return refs, rejected
}

// FormatRefList renders references back into the form ParseRefList accepts, so
// the note editor can round-trip what it stored.
func FormatRefList(refs []Ref) string {
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, r.String())
	}
	return strings.Join(parts, "; ")
}
