package filesystem

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// legacyNameSampleLimit caps how many raw filename bytes are inspected.
// Detection is once per archive, not once per file.
const legacyNameSampleLimit = 64 * 1024

// A zip comes from one machine, so every name that is not already Unicode is
// decoded with the same legacy encoding. Valid UTF-8 is never recoded.
//
// Detection is deterministic and uses only golang.org/x/text. DBCS encodings
// are considered only when some path segment contains two consecutive bytes
// at or above 0x80, so a page that accepts almost every byte (GB18030) cannot
// swallow Western text such as "Müller".
//
// Known misses, left as-is rather than guessed per file: pure-kanji Japanese
// with no kana can be classified as Chinese; an archive that mixes legacy
// encodings is not supported; Korean names that mix Hangul with Hanja are not
// treated as Korean; Japanese names that use only halfwidth katakana are not
// treated as Japanese.

const (
	idGB18030  = "gb18030"
	idBig5     = "big5"
	idShiftJIS = "shiftjis"
	idEUCJP    = "eucjp"
	idEUCKR    = "euckr"
)

type legacyEnc struct {
	id   string
	enc  encoding.Encoding
	rank int
	dbcs bool
	well func([]byte) bool
}

// Lower rank wins a tie. GB18030 is first overall; kana text breaks that tie
// toward Shift-JIS and EUC-JP inside scoreTie.
var legacyCandidates = []legacyEnc{
	{id: idGB18030, enc: simplifiedchinese.GB18030, rank: 0, dbcs: true, well: gb18030WellFormed},
	{id: idShiftJIS, enc: japanese.ShiftJIS, rank: 1, dbcs: true, well: shiftJISWellFormed},
	{id: idBig5, enc: traditionalchinese.Big5, rank: 2, dbcs: true, well: big5WellFormed},
	{id: idEUCKR, enc: korean.EUCKR, rank: 3, dbcs: true, well: eucKRWellFormed},
	{id: idEUCJP, enc: japanese.EUCJP, rank: 4, dbcs: true, well: eucJPWellFormed},
	{id: "windows1252", enc: charmap.Windows1252, rank: 5},
	{id: "cp850", enc: charmap.CodePage850, rank: 6},
	{id: "cp437", enc: charmap.CodePage437, rank: 7},
	{id: "windows1251", enc: charmap.Windows1251, rank: 8},
	{id: "koi8r", enc: charmap.KOI8R, rank: 9},
	{id: "cp866", enc: charmap.CodePage866, rank: 10},
	{id: "windows1250", enc: charmap.Windows1250, rank: 11},
	{id: "windows1253", enc: charmap.Windows1253, rank: 12},
	{id: "windows1254", enc: charmap.Windows1254, rank: 13},
	{id: "windows1255", enc: charmap.Windows1255, rank: 14},
	{id: "windows1256", enc: charmap.Windows1256, rank: 15},
	{id: "windows1257", enc: charmap.Windows1257, rank: 16},
	{id: "windows1258", enc: charmap.Windows1258, rank: 17},
	{id: "windows874", enc: charmap.Windows874, rank: 18},
}

// detectLegacyEncoding returns the encoding to use for rawNames, or nil when
// nothing decodes to a recognizable script. Names that are already valid
// UTF-8 are ignored.
func detectLegacyEncoding(rawNames []string) encoding.Encoding {
	names := sampleLegacyNames(rawNames)
	if len(names) == 0 {
		return nil
	}
	dbcs := false
	for _, n := range names {
		if hasStrongPair(n) {
			dbcs = true
			break
		}
	}

	var best encoding.Encoding
	bestScore := 0
	bestTie := 0
	found := false
	for i := range legacyCandidates {
		c := &legacyCandidates[i]
		if c.dbcs && !dbcs {
			continue
		}
		decoded := make([]string, 0, len(names))
		var counts scriptCount
		ok := true
		for _, n := range names {
			text, good := c.accept(n)
			if !good {
				ok = false
				break
			}
			decoded = append(decoded, text)
			counts.add(text)
		}
		if !ok {
			continue
		}
		group := counts.group()
		if group == "" {
			continue
		}
		score := counts.score(group)
		if group == groupHan {
			score += hanRepertoireBonus(c.id, joinDecoded(decoded), counts.han, counts.hanCommon)
		}
		if score <= 0 {
			continue
		}
		tie := scoreTie(group, c.rank)
		if !found || score > bestScore || (score == bestScore && tie < bestTie) {
			best = c.enc
			bestScore = score
			bestTie = tie
			found = true
		}
	}
	if !found {
		return nil
	}
	return best
}

func sampleLegacyNames(rawNames []string) []string {
	out := make([]string, 0, len(rawNames))
	total := 0
	for _, n := range rawNames {
		if n == "" || utf8.ValidString(n) {
			continue
		}
		if len(out) > 0 && total+len(n) > legacyNameSampleLimit {
			break
		}
		out = append(out, n)
		total += len(n)
		if total >= legacyNameSampleLimit {
			break
		}
	}
	return out
}

func joinDecoded(parts []string) string {
	return strings.Join(parts, "/")
}

func (c *legacyEnc) accept(raw string) (string, bool) {
	if c.well != nil && !c.well([]byte(raw)) {
		return "", false
	}
	text, ok := roundTrip(c.enc, raw)
	if !ok || containsControl(text) {
		return "", false
	}
	return text, true
}

func roundTrip(e encoding.Encoding, raw string) (string, bool) {
	dec, err := e.NewDecoder().Bytes([]byte(raw))
	if err != nil || !utf8.Valid(dec) {
		return "", false
	}
	text := string(dec)
	if strings.ContainsRune(text, '\uFFFD') {
		return "", false
	}
	back, err := e.NewEncoder().Bytes(dec)
	if err != nil || string(back) != raw {
		return "", false
	}
	return text, true
}

func containsControl(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

// hasStrongPair reports whether any slash-separated segment contains two
// consecutive bytes at or above 0x80. Müller (FC 6C) and año (A4 6F) do not.
func hasStrongPair(raw string) bool {
	b := []byte(raw)
	start := 0
	for i := 0; i <= len(b); i++ {
		if i == len(b) || b[i] == '/' || b[i] == '\\' {
			if segmentStrongPair(b[start:i]) {
				return true
			}
			start = i + 1
		}
	}
	return false
}

func segmentStrongPair(seg []byte) bool {
	for i := 0; i+1 < len(seg); i++ {
		if seg[i] >= 0x80 && seg[i+1] >= 0x80 {
			return true
		}
	}
	return false
}

const (
	groupHan    = "han"
	groupHangul = "hangul"
	groupKana   = "kana"
	groupCyr    = "cyr"
	groupLatin  = "latin"
	groupGreek  = "greek"
	groupHebrew = "hebrew"
	groupArabic = "arabic"
	groupThai   = "thai"
)

type scriptCount struct {
	han, hanCommon       int
	hangul, hangulCommon int
	kanaFull, kanaHalf   int
	latin, cyr, cyrFreq  int
	greek, greekFreq     int
	hebrew, hebrewFreq   int
	arabic, arabicFreq   int
	thai, thaiFreq       int
	symbol, box          int
}

func (s *scriptCount) add(text string) {
	for _, r := range text {
		switch {
		case r < 0x80:
			// ASCII, including path separators, does not vote for a script.
			// Otherwise "world/customnpcs/dialogs/河北每日" looks Western.
		case r >= 0xFF61 && r <= 0xFF9F:
			s.kanaHalf++
		case r >= 0x3040 && r <= 0x30FF:
			s.kanaFull++
		case unicode.Is(unicode.Hangul, r):
			s.hangul++
			if _, ok := commonHangul[r]; ok {
				s.hangulCommon++
			}
		case unicode.Is(unicode.Han, r):
			s.han++
			if _, ok := commonHan[r]; ok {
				s.hanCommon++
			}
		case unicode.Is(unicode.Cyrillic, r):
			s.cyr++
			if w, ok := cyrFreq[unicode.ToLower(r)]; ok {
				s.cyrFreq += w
			} else {
				// Ё is rare. Every other unlisted letter is not Russian
				// (Ukrainian і, Serbian ѕ, …) and is typical of mojibake.
				if unicode.ToLower(r) == 'ё' {
					s.cyrFreq++
				} else {
					s.symbol++
				}
			}
		case unicode.Is(unicode.Greek, r):
			s.greek++
			s.greekFreq += greekFreq[unicode.ToLower(r)]
		case unicode.Is(unicode.Hebrew, r):
			s.hebrew++
			s.hebrewFreq += hebrewFreq[r]
		case unicode.Is(unicode.Arabic, r):
			s.arabic++
			s.arabicFreq += arabicFreq[r]
		case unicode.Is(unicode.Thai, r):
			s.thai++
			s.thaiFreq += thaiFreq[r]
		case r >= 0x00C0 && r <= 0x024F:
			s.latin++
		case r >= 0x2500 && r <= 0x259F:
			s.box++
		default:
			s.symbol++
		}
	}
}

func (s scriptCount) group() string {
	switch {
	case s.kanaFull > 0 && s.hangul == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0 && s.latin == 0 &&
		(s.han == 0 || s.kanaFull*2 >= s.han):
		return groupKana
	case s.hangul > 0 && s.han == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0:
		return groupHangul
	case s.han > 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.hangul == 0 && s.latin == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0:
		return groupHan
	case s.cyr > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0:
		return groupCyr
	case s.latin > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0:
		return groupLatin
	case s.greek > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.cyr == 0 && s.hebrew == 0 && s.arabic == 0 && s.thai == 0:
		return groupGreek
	case s.hebrew > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.cyr == 0 && s.greek == 0 && s.arabic == 0 && s.thai == 0:
		return groupHebrew
	case s.arabic > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.thai == 0:
		return groupArabic
	case s.thai > 0 && s.han == 0 && s.hangul == 0 && s.kanaFull == 0 && s.kanaHalf == 0 && s.latin == 0 && s.cyr == 0 && s.greek == 0 && s.hebrew == 0 && s.arabic == 0:
		return groupThai
	default:
		return ""
	}
}

func (s scriptCount) score(group string) int {
	switch group {
	case groupHan:
		// Common characters are what separate real Chinese names from Han
		// mojibake. Obscure Han (a wrong decoder's output) stays at 10 each.
		return s.han*10 + s.hanCommon*20
	case groupHangul:
		return s.hangul*10 + s.hangulCommon*20
	case groupKana:
		return s.kanaFull*30 + s.han*4
	case groupCyr:
		// Frequency is only added for a word-sized run. One Cyrillic letter
		// (Müller misread as ь) must not outscore one Latin letter.
		freq := 0
		if s.cyr >= 4 {
			freq = s.cyrFreq
		}
		return s.cyr*10 + freq - s.symbol*25 - s.box*40
	case groupLatin:
		return s.latin*10 - s.symbol*25 - s.box*40
	case groupGreek:
		freq := 0
		if s.greek >= 4 {
			freq = s.greekFreq
		}
		return s.greek*10 + freq - s.symbol*25 - s.box*40
	case groupHebrew:
		freq := 0
		if s.hebrew >= 4 {
			freq = s.hebrewFreq
		}
		return s.hebrew*10 + freq - s.symbol*25 - s.box*40
	case groupArabic:
		freq := 0
		if s.arabic >= 4 {
			freq = s.arabicFreq
		}
		return s.arabic*10 + freq - s.symbol*25 - s.box*40
	case groupThai:
		freq := 0
		if s.thai >= 4 {
			freq = s.thaiFreq
		}
		return s.thai*10 + freq - s.symbol*25 - s.box*40
	default:
		return 0
	}
}

// scoreTie breaks equal scores. Kana prefers Japanese encodings over GB18030
// because EUC-JP katakana is sometimes also well-formed GB18030.
func scoreTie(group string, rank int) int {
	if group != groupKana {
		return rank
	}
	switch rank {
	case 1: // Shift-JIS
		return -3
	case 4: // EUC-JP
		return -2
	case 0: // GB18030
		return -1
	default:
		return rank
	}
}

// hanRepertoireBonus separates simplified Chinese from Big5 inside the Han
// group only, so it cannot outrank Hangul or kana. HZ is applied only when
// at least two characters are common simplified Han: a single common glyph
// such as 是 shows up in mojibake and must not collect the bonus.
func hanRepertoireBonus(id, text string, han, common int) int {
	switch id {
	case idGB18030:
		if common >= 2 && canEncode(simplifiedchinese.HZGB2312, text) {
			return 25
		}
	case idBig5:
		// Two Han characters are not enough: "Zażółć" round-trips through
		// Big5 as Han mojibake. A real traditional name is longer.
		if han >= 3 && canEncode(traditionalchinese.Big5, text) && !canEncode(simplifiedchinese.HZGB2312, text) {
			return 25
		}
	}
	return 0
}

func canEncode(e encoding.Encoding, s string) bool {
	_, err := e.NewEncoder().String(s)
	return err == nil
}

// commonHanText is a small prior of everyday simplified characters, including
// the names that show up in Chinese game archives (河北、对话、世界、文件夹).
// Obscure Han produced by a wrong decoder is left out on purpose.
const commonHanText = "的一是不了在人有我他这个们中来上大为和国地到以说时要就出会可也你对生能而子那得于着下自之年过发后作里用道行所然家种事成方多经么去法学如都同现当没动面起看定天分还进好小部其些主样理心她本前开但因只从想实日者意无力它与长把机十民第公此已工使情明性知全三又关点正业外将两高间由问很最重并物手应战向头文体政美相见被利什二等产或新己制身果加月话文件夹世界自定义河北每日对"

// Common Hangul syllables. The list is a small prior, not a language model.
// It includes 한 and 글. It deliberately omits syllables that EUC-KR produces
// when it misreads GBK (붉괴첼휑, 썹숭셸).
const commonHangulText = "은는이가을를에의하고도서시수있없한요합니다음것들과또는저우리사람일때말보오아자면게데네세제리미기글파일테스트입니다"

var commonHangul = runeSet(commonHangulText)
var commonHan = runeSet(commonHanText)

func runeSet(s string) map[rune]struct{} {
	m := make(map[rune]struct{}, len(s))
	for _, r := range s {
		m[r] = struct{}{}
	}
	return m
}

// Russian letter frequencies, lowercase. Ё and ў are omitted on purpose so a
// wrong Cyrillic page that leans on them scores lower.
var cyrFreq = map[rune]int{
	'о': 11, 'е': 8, 'а': 8, 'и': 7, 'н': 7, 'т': 6, 'с': 5, 'р': 5,
	'л': 5, 'в': 4, 'к': 3, 'м': 3, 'д': 3, 'п': 3, 'у': 2, 'я': 2,
	'ы': 2, 'ь': 2, 'з': 2, 'б': 2, 'г': 2, 'ч': 1, 'й': 1, 'х': 1,
	'ж': 1, 'ш': 1, 'ю': 1, 'ц': 1, 'щ': 1, 'э': 1, 'ф': 1,
}

// greekFreq is a rough modern-Greek prior. Accented vowels are included so
// Ελληνικά scores above a Cyrillic misread of the same bytes. A Greek misread
// of Привет stays below that word's Russian score.
var greekFreq = map[rune]int{
	'α': 12, 'ά': 12, 'ε': 10, 'έ': 10, 'ι': 8, 'ί': 8, 'ϊ': 4, 'ΐ': 4,
	'ο': 8, 'ό': 8, 'η': 6, 'ή': 6, 'υ': 4, 'ύ': 4, 'ϋ': 2, 'ΰ': 2,
	'ω': 3, 'ώ': 3, 'τ': 8, 'σ': 7, 'ς': 7, 'ν': 6, 'ρ': 5, 'π': 3,
	'κ': 4, 'μ': 4, 'λ': 4, 'γ': 2, 'δ': 2, 'θ': 1, 'β': 1, 'φ': 1,
	'χ': 1, 'ξ': 1, 'ψ': 1, 'ζ': 1,
}

// hebrewFreq covers שלום without rewarding a pointed misread very highly.
var hebrewFreq = map[rune]int{
	'י': 10, 'ו': 8, 'ה': 7, 'ל': 6, 'א': 5, 'ר': 5, 'ב': 5, 'מ': 4, 'ם': 5,
	'ש': 6, 'ת': 4, 'נ': 3, 'ן': 3, 'ד': 3, 'כ': 2, 'ך': 2, 'ע': 2, 'ח': 2,
	'ק': 2, 'פ': 2, 'ף': 2, 'ס': 2, 'ז': 1, 'ג': 1, 'צ': 1, 'ץ': 1, 'ט': 1,
}

// arabicFreq covers مرحبا. Letters outside the map add nothing.
var arabicFreq = map[rune]int{
	'ا': 8, 'أ': 6, 'إ': 4, 'آ': 3, 'ل': 7, 'م': 5, 'ر': 6, 'ب': 4, 'ح': 3,
	'ن': 4, 'ي': 5, 'ى': 3, 'و': 4, 'ت': 3, 'د': 2, 'س': 2, 'ك': 2, 'ة': 2,
	'ه': 3, 'ع': 2, 'ف': 2, 'ق': 2, 'ج': 1, 'خ': 1, 'ذ': 1, 'ش': 1, 'ص': 1,
	'ض': 1, 'ط': 1, 'ظ': 1, 'غ': 1, 'ز': 1, 'ث': 1,
}

// thaiFreq boosts everyday Thai (สวัสดี) above a Greek misread of the same
// bytes. The mojibake Thai that GBK produces barely overlaps this set, so it
// does not outscore real simplified Chinese.
var thaiFreq = map[rune]int{
	'ส': 6, 'ว': 4, 'ั': 4, 'ด': 4, 'ี': 6, 'ก': 4, 'า': 5, 'น': 4, 'ม': 3,
	'ร': 3, 'เ': 4, '่': 3, '้': 3, 'ท': 3, 'ง': 3, 'อ': 3, 'ย': 3, 'ล': 2,
	'ค': 2, 'ป': 2, 'ช': 2, 'บ': 2, 'ต': 2,
}

func gb18030WellFormed(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c <= 0x7F || c == 0x80 {
			i++
			continue
		}
		if c < 0x81 || c > 0xFE || i+1 >= len(b) {
			return false
		}
		c2 := b[i+1]
		if c2 >= 0x30 && c2 <= 0x39 {
			if i+3 >= len(b) {
				return false
			}
			c3, c4 := b[i+2], b[i+3]
			if c3 < 0x81 || c3 > 0xFE || c4 < 0x30 || c4 > 0x39 {
				return false
			}
			i += 4
			continue
		}
		if (c2 >= 0x40 && c2 <= 0x7E) || (c2 >= 0x80 && c2 <= 0xFE) {
			i += 2
			continue
		}
		return false
	}
	return true
}

func big5WellFormed(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c <= 0x7F {
			i++
			continue
		}
		if c < 0x81 || c > 0xFE || i+1 >= len(b) {
			return false
		}
		c2 := b[i+1]
		if (c2 >= 0x40 && c2 <= 0x7E) || (c2 >= 0xA1 && c2 <= 0xFE) {
			i += 2
			continue
		}
		return false
	}
	return true
}

func shiftJISWellFormed(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c <= 0x7F || (c >= 0xA1 && c <= 0xDF) {
			i++
			continue
		}
		if i+1 >= len(b) || !((c >= 0x81 && c <= 0x9F) || (c >= 0xE0 && c <= 0xFC)) {
			return false
		}
		c2 := b[i+1]
		if (c2 >= 0x40 && c2 <= 0x7E) || (c2 >= 0x80 && c2 <= 0xFC) {
			i += 2
			continue
		}
		return false
	}
	return true
}

func eucJPWellFormed(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c <= 0x7F {
			i++
			continue
		}
		if c == 0x8E && i+1 < len(b) && b[i+1] >= 0xA1 && b[i+1] <= 0xDF {
			i += 2
			continue
		}
		if c == 0x8F && i+2 < len(b) && b[i+1] >= 0xA1 && b[i+1] <= 0xFE && b[i+2] >= 0xA1 && b[i+2] <= 0xFE {
			i += 3
			continue
		}
		if c >= 0xA1 && c <= 0xFE && i+1 < len(b) && b[i+1] >= 0xA1 && b[i+1] <= 0xFE {
			i += 2
			continue
		}
		return false
	}
	return true
}

func eucKRWellFormed(b []byte) bool {
	for i := 0; i < len(b); {
		c := b[i]
		if c <= 0x7F {
			i++
			continue
		}
		if c >= 0xA1 && c <= 0xFE && i+1 < len(b) && b[i+1] >= 0xA1 && b[i+1] <= 0xFE {
			i += 2
			continue
		}
		return false
	}
	return true
}
