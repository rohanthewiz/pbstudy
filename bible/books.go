// Package bible owns everything about scripture: the canon's structure,
// reference parsing, verse queries, and the downloader that fills the cache.
package bible

import (
	"strings"

	"github.com/rohanthewiz/serr"
)

// Testament values.
const (
	OldTestament = "OT"
	NewTestament = "NT"
)

// MaxChapterVerses is the verse count of the longest chapter in scripture
// (Psalm 119). It is a sanity bound, not a lookup table: code that expands a
// stored verse range clamps to it so a hand-edited URL or a corrupt row cannot
// ask for an unbounded loop. Real per-chapter verse counts come from the
// scripture cache, not from here.
const MaxChapterVerses = 176

// Book describes one book of the 66-book Protestant canon.
//
// Four different naming systems meet here, which is exactly why this table
// exists in one place:
//
//	Num        1..66, the ordering getbible.net uses in its per-book URLs
//	Name       the display name
//	OSIS       the interchange standard (Gen, 1Sam, Phlm) — for future export
//	BLBAbbrev  Blue Letter Bible's URL slug (gen, 1sa, phm) — for deep links
//
// The slugs differ from OSIS in ways no rule predicts (Song → sng not song,
// Jude → jde not jude, James → jam not jas), so they are spelled out rather
// than derived.
type Book struct {
	Num          int
	Name         string
	OSIS         string
	BLBAbbrev    string
	Testament    string
	ChapterCount int
	// Aliases are lowercase forms accepted by the reference parser, beyond
	// the name and the two abbreviations (which are registered
	// automatically). Numeric prefixes are normalized before lookup, so
	// "1st John", "I John" and "1 John" all reach the "1 john" entry.
	Aliases []string
}

// Books is the canon in order. Index by Num-1, or use ByNum.
var Books = []Book{
	{1, "Genesis", "Gen", "gen", OldTestament, 50, []string{"gn"}},
	{2, "Exodus", "Exod", "exo", OldTestament, 40, []string{"ex", "exod"}},
	{3, "Leviticus", "Lev", "lev", OldTestament, 27, []string{"lv"}},
	{4, "Numbers", "Num", "num", OldTestament, 36, []string{"nm", "nb"}},
	{5, "Deuteronomy", "Deut", "deu", OldTestament, 34, []string{"dt", "deut"}},
	{6, "Joshua", "Josh", "jos", OldTestament, 24, []string{"josh"}},
	{7, "Judges", "Judg", "jdg", OldTestament, 21, []string{"judg", "jg"}},
	{8, "Ruth", "Ruth", "rth", OldTestament, 4, []string{"ru"}},
	{9, "1 Samuel", "1Sam", "1sa", OldTestament, 31, []string{"1 sam", "1sam", "1 sm"}},
	{10, "2 Samuel", "2Sam", "2sa", OldTestament, 24, []string{"2 sam", "2sam", "2 sm"}},
	{11, "1 Kings", "1Kgs", "1ki", OldTestament, 22, []string{"1 kgs", "1kgs", "1 kg"}},
	{12, "2 Kings", "2Kgs", "2ki", OldTestament, 25, []string{"2 kgs", "2kgs", "2 kg"}},
	{13, "1 Chronicles", "1Chr", "1ch", OldTestament, 29, []string{"1 chr", "1chr", "1 chron"}},
	{14, "2 Chronicles", "2Chr", "2ch", OldTestament, 36, []string{"2 chr", "2chr", "2 chron"}},
	{15, "Ezra", "Ezra", "ezr", OldTestament, 10, nil},
	{16, "Nehemiah", "Neh", "neh", OldTestament, 13, []string{"ne"}},
	{17, "Esther", "Esth", "est", OldTestament, 10, []string{"esth"}},
	{18, "Job", "Job", "job", OldTestament, 42, []string{"jb"}},
	{19, "Psalms", "Ps", "psa", OldTestament, 150, []string{"psalm", "ps", "pss", "psm"}},
	{20, "Proverbs", "Prov", "pro", OldTestament, 31, []string{"prov", "prv", "pr"}},
	{21, "Ecclesiastes", "Eccl", "ecc", OldTestament, 12, []string{"eccl", "eccles", "qoh"}},
	{22, "Song of Solomon", "Song", "sng", OldTestament, 8, []string{"song", "song of songs", "sos", "canticles", "cant"}},
	{23, "Isaiah", "Isa", "isa", OldTestament, 66, []string{"is"}},
	{24, "Jeremiah", "Jer", "jer", OldTestament, 52, []string{"jr"}},
	{25, "Lamentations", "Lam", "lam", OldTestament, 5, []string{"lm"}},
	{26, "Ezekiel", "Ezek", "eze", OldTestament, 48, []string{"ezek", "ezk"}},
	{27, "Daniel", "Dan", "dan", OldTestament, 12, []string{"dn"}},
	{28, "Hosea", "Hos", "hos", OldTestament, 14, []string{"ho"}},
	{29, "Joel", "Joel", "joe", OldTestament, 3, []string{"jl"}},
	{30, "Amos", "Amos", "amo", OldTestament, 9, []string{"am"}},
	{31, "Obadiah", "Obad", "oba", OldTestament, 1, []string{"obad", "ob"}},
	{32, "Jonah", "Jonah", "jon", OldTestament, 4, []string{"jnh"}},
	{33, "Micah", "Mic", "mic", OldTestament, 7, []string{"mi"}},
	{34, "Nahum", "Nah", "nah", OldTestament, 3, []string{"na"}},
	{35, "Habakkuk", "Hab", "hab", OldTestament, 3, []string{"hb"}},
	{36, "Zephaniah", "Zeph", "zep", OldTestament, 3, []string{"zeph", "zp"}},
	{37, "Haggai", "Hag", "hag", OldTestament, 2, []string{"hg"}},
	{38, "Zechariah", "Zech", "zec", OldTestament, 14, []string{"zech", "zc"}},
	{39, "Malachi", "Mal", "mal", OldTestament, 4, []string{"ml"}},

	{40, "Matthew", "Matt", "mat", NewTestament, 28, []string{"matt", "mt"}},
	{41, "Mark", "Mark", "mar", NewTestament, 16, []string{"mk", "mrk"}},
	{42, "Luke", "Luke", "luk", NewTestament, 24, []string{"lk"}},
	{43, "John", "John", "jhn", NewTestament, 21, []string{"jn"}},
	{44, "Acts", "Acts", "act", NewTestament, 28, []string{"ac"}},
	{45, "Romans", "Rom", "rom", NewTestament, 16, []string{"rm"}},
	{46, "1 Corinthians", "1Cor", "1co", NewTestament, 16, []string{"1 cor", "1cor"}},
	{47, "2 Corinthians", "2Cor", "2co", NewTestament, 13, []string{"2 cor", "2cor"}},
	{48, "Galatians", "Gal", "gal", NewTestament, 6, []string{"ga"}},
	{49, "Ephesians", "Eph", "eph", NewTestament, 6, []string{"ephes"}},
	{50, "Philippians", "Phil", "phl", NewTestament, 4, []string{"phil", "php", "pp"}},
	{51, "Colossians", "Col", "col", NewTestament, 4, []string{"cl"}},
	{52, "1 Thessalonians", "1Thess", "1th", NewTestament, 5, []string{"1 thess", "1thess", "1 thes"}},
	{53, "2 Thessalonians", "2Thess", "2th", NewTestament, 3, []string{"2 thess", "2thess", "2 thes"}},
	{54, "1 Timothy", "1Tim", "1ti", NewTestament, 6, []string{"1 tim", "1tim"}},
	{55, "2 Timothy", "2Tim", "2ti", NewTestament, 4, []string{"2 tim", "2tim"}},
	{56, "Titus", "Titus", "tit", NewTestament, 3, []string{"ti"}},
	{57, "Philemon", "Phlm", "phm", NewTestament, 1, []string{"philem", "phlm"}},
	{58, "Hebrews", "Heb", "heb", NewTestament, 13, []string{"hbr"}},
	{59, "James", "Jas", "jam", NewTestament, 5, []string{"jas", "jm"}},
	{60, "1 Peter", "1Pet", "1pe", NewTestament, 5, []string{"1 pet", "1pet", "1 pt"}},
	{61, "2 Peter", "2Pet", "2pe", NewTestament, 3, []string{"2 pet", "2pet", "2 pt"}},
	{62, "1 John", "1John", "1jn", NewTestament, 5, []string{"1 jn", "1jn", "1 jhn"}},
	{63, "2 John", "2John", "2jn", NewTestament, 1, []string{"2 jn", "2jn", "2 jhn"}},
	{64, "3 John", "3John", "3jn", NewTestament, 1, []string{"3 jn", "3jn", "3 jhn"}},
	{65, "Jude", "Jude", "jde", NewTestament, 1, []string{"jud", "jde"}},
	{66, "Revelation", "Rev", "rev", NewTestament, 22, []string{"revelations", "rv", "apocalypse"}},
}

// bookIndex maps every accepted spelling (already normalized) to a book.
// Built once at init; the parser and the URL handlers both read it.
var bookIndex = map[string]*Book{}

func init() {
	for i := range Books {
		bk := &Books[i]
		// Register the display name, both standard abbreviations, and the
		// hand-written aliases. Collisions would be a data bug, so the
		// later entry simply wins — but the table is curated to avoid them.
		for _, key := range append([]string{bk.Name, bk.OSIS, bk.BLBAbbrev}, bk.Aliases...) {
			bookIndex[normalizeBookKey(key)] = bk
		}
	}
}

// normalizeBookKey folds a user-typed book name into the lookup form:
// lowercase, single-spaced, with ordinal and roman-numeral prefixes reduced
// to a plain digit. "II Corinthians", "2nd Corinthians" and "2 Corinthians"
// all converge on "2 corinthians".
func normalizeBookKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Join(strings.Fields(s), " ") // collapse internal whitespace
	s = strings.ReplaceAll(s, ".", "")       // "Gen." -> "gen"

	// Leading ordinal/roman forms -> digit. Longest first so "iii" is not
	// matched by the "ii" rule.
	for _, r := range []struct{ from, to string }{
		{"iii ", "3 "}, {"ii ", "2 "}, {"i ", "1 "},
		{"third ", "3 "}, {"second ", "2 "}, {"first ", "1 "},
		{"3rd ", "3 "}, {"2nd ", "2 "}, {"1st ", "1 "},
	} {
		if strings.HasPrefix(s, r.from) {
			s = r.to + strings.TrimPrefix(s, r.from)
			break
		}
	}
	// "1john" -> "1 john": insert a space after a leading digit so the
	// spaced and unspaced forms share one key.
	if len(s) > 1 && s[0] >= '1' && s[0] <= '3' && s[1] != ' ' {
		s = s[:1] + " " + s[1:]
	}
	return s
}

// ByNum returns the book with the given 1..66 number.
func ByNum(num int) (*Book, error) {
	if num < 1 || num > len(Books) {
		return nil, serr.New("book number out of range", "num", itoa(num))
	}
	return &Books[num-1], nil
}

// ByName resolves any accepted spelling of a book name to its entry.
func ByName(name string) (*Book, error) {
	if bk, ok := bookIndex[normalizeBookKey(name)]; ok {
		return bk, nil
	}
	return nil, serr.New("unknown book", "name", name)
}

// MustByNum is ByNum for call sites where the number is already known valid
// (e.g. read back from a database column that was written from Books).
func MustByNum(num int) *Book {
	bk, err := ByNum(num)
	if err != nil {
		return &Book{Num: num, Name: "Book " + itoa(num)}
	}
	return bk
}

// itoa avoids pulling strconv into this file for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
