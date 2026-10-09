package speech

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Spoken rewrites written forms (amounts, times, dates, phone numbers, emails,
// abbreviations) into text that Qwen3-TTS reads naturally, in British English.
// It is deterministic and conservative: anything it is not sure about is left
// as written. The chat text is never changed; only the text sent to TTS is.
// The stage directions "(laugh)", "(sigh)", "(cough)" and "(clears throat)" are
// dropped: the model would read them aloud.
func Spoken(text string) string {
	if text == "" {
		return text
	}
	for _, r := range text {
		if r >= 0xE000 && r <= 0xF8FF {
			// Private-use characters are used internally as placeholders.
			return text
		}
	}
	p := &speaker{}
	s := dropStageDirections(text)
	s = zoneRE.ReplaceAllString(s, "$1")
	s = spReplaceAll(s, emailRE, p.email)
	s = spReplaceAll(s, phoneRE, p.phone)
	s = spReplaceAll(s, isoDateRE, p.isoDate)
	s = spReplaceAll(s, ukDateRE, p.ukDate)
	s = spReplaceAll(s, timeRE, p.times)
	s = spReplaceAll(s, moneyPrefixRE, spMoneyPrefix)
	s = spReplaceAll(s, moneySuffixRE, spMoneySuffix)
	s = spReplaceAll(s, percentRE, spPercent)
	s = spReplaceAll(s, abbrevRE, spAbbrev)
	s = spReplaceAll(s, ampersandRE, spAmpersand)
	s = spReplaceAll(s, numberSignRE, spNumberSign)
	s = spReplaceAll(s, rangeRE, spRange)
	s = spReplaceAll(s, numberRE, spPlain)
	s = spReplaceAll(s, formRE, spForm)
	return p.restore(s)
}

const amtPat = `\d{1,3}(?:,\d{3})+(?:\.\d+)?|\d+(?:\.\d+)?`
const intPat = `\d{1,3}(?:,\d{3})+|\d+`
const merPat = `[AaPp]\.?[Mm]\.?`
const timeTok = `\d{1,2}(?:[:.]\d{2}(?:[ \x{00a0}]?` + merPat + `)?|[ \x{00a0}]?` + merPat + `)`
const dashSep = `[ \x{00a0}]?[–—-][ \x{00a0}]?`

var (
	emailRE       = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)+`)
	phoneRE       = regexp.MustCompile(`(?:\+\d+|\b0\d*)(?:(?:[ \-]?\(0\))?[ \-]?\d+)*`)
	isoDateRE     = regexp.MustCompile(`(\d{4})-(\d{2})-(\d{2})`)
	ukDateRE      = regexp.MustCompile(`(\d{1,2})/(\d{1,2})/(\d{4})`)
	timeRE        = regexp.MustCompile(timeTok + `(?:` + dashSep + timeTok + `)?`)
	clockRE       = regexp.MustCompile(`^(\d{1,2})(?:([:.])(\d{2}))?[\s\x{00a0}]*(?:([AaPp])\.?[Mm]\.?)?$`)
	moneyPrefixRE = regexp.MustCompile(`((?i:AED|DHS|DH|USD|US\$|GBP|EUR)|[$£€])[ \x{00a0}]?(` + amtPat + `)(?:` + dashSep + `(` + amtPat + `))?(?:[ \x{00a0}]((?i:million|billion|trillion|thousand)))?`)
	moneySuffixRE = regexp.MustCompile(`(` + amtPat + `)(?:` + dashSep + `(` + amtPat + `))?[ \x{00a0}]?((?i:AED|DHS|DH|USD|GBP|EUR|dirhams?))`)
	percentRE     = regexp.MustCompile(`(\d+(?:[.,]\d+)*)(?:(?:[ \x{00a0}]?–[ \x{00a0}]?|-)(\d+(?:[.,]\d+)*))?[ \x{00a0}]?%`)
	abbrevRE      = regexp.MustCompile(`(?i)\b(?:e\.g\.|i\.e\.|approx\.?|vs\.?)`)
	ampersandRE   = regexp.MustCompile(`&`)
	numberSignRE  = regexp.MustCompile(`\bNo\.[ \x{00a0}]?(\d)`)
	rangeRE       = regexp.MustCompile(`(` + intPat + `)(?:[ \x{00a0}]?–[ \x{00a0}]?|-)(` + intPat + `)`)
	numberRE      = regexp.MustCompile(amtPat)
	formRE        = regexp.MustCompile(`FS([12])`)
)

// zoneRE drops a time zone label after a time ("2:30 PM UTC", "9:00 am (Dubai
// time)"): people do not say it aloud, and TTS would spell out "UTC".
var zoneRE = regexp.MustCompile(`(?i)(\d(?:[ \x{00a0}]?[ap]\.?m\.?)?)[ \x{00a0}]+\(?(?:UTC|GMT|GST|Dubai time|local time)\)?`)

var stageDirectionRE = regexp.MustCompile(`(?i)\((?:laugh|cough|clears throat|sigh)\)`)
var doubleSpaceRE = regexp.MustCompile(` {2,}`)

// dropStageDirections removes the bracketed vocal cues some language models add
// out of habit, and the double space they leave behind.
func dropStageDirections(text string) string {
	if !stageDirectionRE.MatchString(text) {
		return text
	}
	return strings.TrimSpace(doubleSpaceRE.ReplaceAllString(stageDirectionRE.ReplaceAllString(text, ""), " "))
}

// speaker keeps finished spoken fragments out of reach of later passes by
// swapping them for private-use placeholders until the end.
type speaker struct{ held []string }

func (p *speaker) hold(spoken string) string {
	if len(p.held) >= 0x1800 {
		return spoken
	}
	p.held = append(p.held, spoken)
	idx := 0xE100 + len(p.held) - 1
	return "" + string(rune(idx)) + ""
}

func (p *speaker) restore(s string) string {
	for i, v := range p.held {
		idx := 0xE100 + i
		s = strings.ReplaceAll(s, ""+string(rune(idx))+"", v)
	}
	return s
}

// spReplaceAll applies fn to every match of re. fn returns the replacement and
// whether to use it; it may extend loc[1] to consume trailing text.
func spReplaceAll(s string, re *regexp.Regexp, fn func(s string, loc []int) (string, bool)) string {
	locs := re.FindAllStringSubmatchIndex(s, -1)
	if locs == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, loc := range locs {
		out, ok := fn(s, loc)
		if !ok {
			continue
		}
		b.WriteString(s[last:loc[0]])
		b.WriteString(out)
		last = loc[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func spGroup(s string, loc []int, n int) string {
	if 2*n+1 >= len(loc) || loc[2*n] < 0 {
		return ""
	}
	return s[loc[2*n]:loc[2*n+1]]
}

func runeBefore(s string, i int) rune {
	if i <= 0 {
		return 0
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func runeAfter(s string, j int) rune {
	if j < 0 || j >= len(s) {
		return 0
	}
	r, _ := utf8.DecodeRuneInString(s[j:])
	return r
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// okLeft reports whether a match starting at i is not glued to a preceding
// word, id, decimal or other number.
func okLeft(s string, i int) bool {
	b := runeBefore(s, i)
	switch {
	case isWordRune(b), b == '#', b == '+', b == '@':
		return false
	case b == '-':
		return !isWordRune(runeBefore(s, i-1))
	case b == '.', b == ',', b == ':', b == '/':
		return !unicode.IsDigit(runeBefore(s, i-1))
	}
	return true
}

// okRight is okLeft's counterpart for the end j of a match.
func okRight(s string, j int) bool {
	a := runeAfter(s, j)
	switch {
	case isWordRune(a):
		return false
	case a == '-':
		return !isWordRune(runeAfter(s, j+1))
	case a == '.', a == ',', a == ':', a == '/':
		return !unicode.IsDigit(runeAfter(s, j+1))
	}
	return true
}

var numOnes = [...]string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}

var numTens = [...]string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

type numScale struct {
	size int64
	name string
}

var numScales = []numScale{
	{1_000_000_000_000, "trillion"},
	{1_000_000_000, "billion"},
	{1_000_000, "million"},
	{1000, "thousand"},
}

// spIntWords spells n in British English ("one hundred and five"). Values
// must be below one quadrillion.
func spIntWords(n int64) string {
	if n == 0 {
		return numOnes[0]
	}
	var parts []string
	for _, sc := range numScales {
		if n >= sc.size {
			parts = append(parts, spBelowThousand(n/sc.size)+" "+sc.name)
			n %= sc.size
		}
	}
	if n > 0 {
		last := spBelowThousand(n)
		if len(parts) > 0 && n < 100 {
			last = "and " + last
		}
		parts = append(parts, last)
	}
	return strings.Join(parts, " ")
}

func spBelowThousand(n int64) string {
	var out string
	if h := n / 100; h > 0 {
		out = numOnes[h] + " hundred"
		n %= 100
		if n > 0 {
			out += " and "
		}
	}
	switch {
	case n == 0:
	case n < 20:
		out += numOnes[n]
	default:
		out += numTens[n/10]
		if n%10 > 0 {
			out += "-" + numOnes[n%10]
		}
	}
	return out
}

// spDigits reads each digit on its own: "586" becomes "five eight six".
func spDigits(s string) string {
	var words []string
	for _, r := range s {
		if r >= '0' && r <= '9' {
			words = append(words, numOnes[r-'0'])
		}
	}
	return strings.Join(words, " ")
}

// spParseInt parses plain digits; leading zeros (codes) and huge values fail.
func spParseInt(s string) (int64, bool) {
	if s == "" || len(s) > 15 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil
}

// spSpeakNum spells numbers with thousands separators or 5+ digits and leaves
// small numbers as digits, which TTS reads fine.
func spSpeakNum(tok string) (string, bool) {
	digits := strings.ReplaceAll(tok, ",", "")
	if !strings.Contains(tok, ",") && len(digits) < 5 {
		return tok, true
	}
	n, ok := spParseInt(digits)
	if !ok {
		return "", false
	}
	return spIntWords(n), true
}

func spPlain(s string, loc []int) (string, bool) {
	tok := s[loc[0]:loc[1]]
	if strings.Contains(tok, ".") || !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	return spSpeakNum(tok)
}

func spRange(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	l, r := spGroup(s, loc, 1), spGroup(s, loc, 2)
	ln, err1 := strconv.ParseInt(strings.ReplaceAll(l, ",", ""), 10, 64)
	rn, err2 := strconv.ParseInt(strings.ReplaceAll(r, ",", ""), 10, 64)
	if err1 != nil || err2 != nil || ln >= rn {
		return "", false
	}
	// 586-2700 looks like a local phone number, not a range.
	if len(l) == 3 && len(r) == 4 {
		return "", false
	}
	lw, ok1 := spSpeakNum(l)
	rw, ok2 := spSpeakNum(r)
	if !ok1 || !ok2 {
		return "", false
	}
	return lw + " to " + rw, true
}

func spPercent(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	out := spGroup(s, loc, 1)
	if hi := spGroup(s, loc, 2); hi != "" {
		out += " to " + hi
	}
	return out + " percent", true
}

// Currency.

type spCurrency struct{ one, many string }

func spCurrencyOf(code string) (spCurrency, bool) {
	switch strings.ToUpper(code) {
	case "AED", "DH", "DHS", "DIRHAM", "DIRHAMS":
		return spCurrency{"dirham", "dirhams"}, true
	case "USD", "US$", "$":
		return spCurrency{"dollar", "dollars"}, true
	case "GBP", "£":
		return spCurrency{"pound", "pounds"}, true
	case "EUR", "€":
		return spCurrency{"euro", "euros"}, true
	}
	return spCurrency{}, false
}

type spAmount struct {
	whole int64
	frac  string
}

// spParseAmount reads "51,917" or "12.50". A zero fraction is dropped and a
// fraction of more than two digits is not an amount of money.
func spParseAmount(s string) (spAmount, bool) {
	whole, frac, _ := strings.Cut(strings.ReplaceAll(s, ",", ""), ".")
	n, ok := spParseInt(whole)
	if !ok {
		return spAmount{}, false
	}
	a := spAmount{whole: n}
	if strings.Trim(frac, "0") != "" {
		if len(frac) > 2 {
			return spAmount{}, false
		}
		if len(frac) == 1 {
			frac += "0"
		}
		if frac[0] == '0' {
			a.frac = "oh " + numOnes[frac[1]-'0']
		} else {
			a.frac = spIntWords(int64(frac[0]-'0')*10 + int64(frac[1]-'0'))
		}
	}
	return a, true
}

// bare is the amount without a currency word.
func (a spAmount) bare() string {
	out := spIntWords(a.whole)
	if a.frac != "" {
		out += " " + a.frac
	}
	return out
}

// say puts the currency word after the whole part: "twelve dirhams fifty".
func (a spAmount) say(c spCurrency) string {
	unit := c.many
	if a.whole == 1 {
		unit = c.one
	}
	out := spIntWords(a.whole) + " " + unit
	if a.frac != "" {
		out += " " + a.frac
	}
	return out
}

func spMoney(c spCurrency, lo, hi, scale string) (string, bool) {
	if scale != "" {
		if hi != "" {
			return "", false
		}
		whole, frac, _ := strings.Cut(strings.ReplaceAll(lo, ",", ""), ".")
		n, ok := spParseInt(whole)
		if !ok {
			return "", false
		}
		out := spIntWords(n)
		if frac = strings.TrimRight(frac, "0"); frac != "" {
			out += " point " + spDigits(frac)
		}
		return out + " " + strings.ToLower(scale) + " " + c.many, true
	}
	a, ok := spParseAmount(lo)
	if !ok {
		return "", false
	}
	if hi == "" {
		return a.say(c), true
	}
	b, ok := spParseAmount(hi)
	if !ok {
		return "", false
	}
	return a.bare() + " to " + b.say(c), true
}

func spMoneyPrefix(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	c, ok := spCurrencyOf(spGroup(s, loc, 1))
	if !ok {
		return "", false
	}
	return spMoney(c, spGroup(s, loc, 2), spGroup(s, loc, 3), spGroup(s, loc, 4))
}

func spMoneySuffix(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	c, ok := spCurrencyOf(spGroup(s, loc, 3))
	if !ok {
		return "", false
	}
	return spMoney(c, spGroup(s, loc, 1), spGroup(s, loc, 2), "")
}

// Times.

type clock struct {
	h, m int
	mer  byte
	two  bool
}

// parseClock reads "09:00", "9.30am" or "5 pm". A bare "9.30" is not a time.
func parseClock(tok string) (clock, bool) {
	m := clockRE.FindStringSubmatch(tok)
	if m == nil {
		return clock{}, false
	}
	c := clock{two: len(m[1]) == 2}
	c.h, _ = strconv.Atoi(m[1])
	if m[3] != "" {
		c.m, _ = strconv.Atoi(m[3])
	}
	if m[4] != "" {
		c.mer = byte(unicode.ToLower(rune(m[4][0])))
	}
	if m[3] == "" && c.mer == 0 {
		return clock{}, false
	}
	if m[2] == "." && c.mer == 0 {
		return clock{}, false
	}
	if c.m > 59 {
		return clock{}, false
	}
	if c.mer != 0 {
		if c.h < 1 || c.h > 12 {
			return clock{}, false
		}
	} else if c.h > 23 {
		return clock{}, false
	}
	return c, true
}

// say renders the time. Without a meridiem the hour is read as 24-hour only
// when it is written with two digits (or force24 is set by a sibling time);
// "1:30" alone is ambiguous and stays as written.
func (c clock) say(force24 bool) (string, bool) {
	h, mer := c.h, c.mer
	if mer == 0 {
		if !c.two && !force24 {
			return "", false
		}
		switch {
		case h == 0 && c.m == 0:
			return "midnight", true
		case h == 12 && c.m == 0:
			return "12 noon", true
		case h < 12:
			mer = 'a'
			if h == 0 {
				h = 12
			}
		default:
			mer = 'p'
			if h > 12 {
				h -= 12
			}
		}
	} else if h == 12 && c.m == 0 {
		if mer == 'p' {
			return "12 noon", true
		}
		return "midnight", true
	}
	suffix := "a.m."
	if mer == 'p' {
		suffix = "p.m."
	}
	if c.m != 0 {
		return fmt.Sprintf("%d:%02d %s", h, c.m, suffix), true
	}
	return fmt.Sprintf("%d %s", h, suffix), true
}

func (p *speaker) times(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	txt := s[loc[0]:loc[1]]
	left, right := txt, ""
	if i := strings.IndexAny(txt, "–—-"); i >= 0 {
		_, w := utf8.DecodeRuneInString(txt[i:])
		left, right = strings.TrimSpace(txt[:i]), strings.TrimSpace(txt[i+w:])
	}
	l, ok := parseClock(left)
	if !ok {
		return "", false
	}
	var out string
	if right == "" {
		if out, ok = l.say(false); !ok {
			return "", false
		}
	} else {
		r, ok2 := parseClock(right)
		if !ok2 {
			return "", false
		}
		force := l.mer == 0 && r.mer == 0 && (l.two || r.two)
		lw, lok := l.say(force)
		rw, rok := r.say(force)
		if !lok && !rok {
			return "", false
		}
		if !lok {
			lw = left
		}
		if !rok {
			rw = right
		}
		out = lw + " to " + rw
	}
	// "a.m." already ends the sentence; do not leave a doubled full stop.
	if strings.HasSuffix(out, ".") && runeAfter(s, loc[1]) == '.' && runeAfter(s, loc[1]+1) != '.' {
		loc[1]++
	}
	return p.hold(out), true
}

// Dates.

func spDate(y, m, d int) (string, bool) {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	if y < 1000 || t.Year() != y || int(t.Month()) != m || t.Day() != d {
		return "", false
	}
	return fmt.Sprintf("%d %s %d", d, time.Month(m), y), true
}

func (p *speaker) date(s string, loc []int, yi, mi, di int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	y, _ := strconv.Atoi(spGroup(s, loc, yi))
	m, _ := strconv.Atoi(spGroup(s, loc, mi))
	d, _ := strconv.Atoi(spGroup(s, loc, di))
	out, ok := spDate(y, m, d)
	if !ok {
		return "", false
	}
	return p.hold(out), true
}

func (p *speaker) isoDate(s string, loc []int) (string, bool) { return p.date(s, loc, 1, 2, 3) }

// ukDate reads d/m/y.
func (p *speaker) ukDate(s string, loc []int) (string, bool) { return p.date(s, loc, 3, 2, 1) }

// Phone numbers and emails.

// phone speaks a clear phone number (leading + or 0, 9 to 13 digits) in
// groups: "+971 4 586 2700" becomes "plus nine seven one, four, five eight six, two seven zero zero".
func (p *speaker) phone(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	tok := strings.ReplaceAll(s[loc[0]:loc[1]], "(0)", " ")
	digits := 0
	for _, r := range tok {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits < 9 || digits > 13 {
		return "", false
	}
	var parts []string
	for _, f := range strings.FieldsFunc(tok, func(r rune) bool { return r == ' ' || r == '-' }) {
		prefix := ""
		if strings.HasPrefix(f, "+") {
			prefix = "plus "
			f = f[1:]
		}
		parts = append(parts, prefix+spDigits(f))
	}
	return p.hold(strings.Join(parts, ", ")), true
}

var emailWords = strings.NewReplacer(".", " dot ", "_", " underscore ", "-", " dash ")

func (p *speaker) email(s string, loc []int) (string, bool) {
	local, domain, ok := strings.Cut(s[loc[0]:loc[1]], "@")
	if !ok {
		return "", false
	}
	return p.hold(emailWords.Replace(local) + " at " + emailWords.Replace(domain)), true
}

// Abbreviations.

func spAbbrev(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	tok := s[loc[0]:loc[1]]
	var out string
	switch strings.ToLower(strings.TrimSuffix(tok, ".")) {
	case "e.g":
		out = "for example"
	case "i.e":
		out = "that is"
	case "approx":
		out = "approximately"
	case "vs":
		out = "versus"
	default:
		return "", false
	}
	if r, _ := utf8.DecodeRuneInString(tok); unicode.IsUpper(r) {
		out = strings.ToUpper(out[:1]) + out[1:]
	}
	return out, true
}

// spAmpersand only rewrites a free-standing "&", never "R&D" or "Q&A".
func spAmpersand(s string, loc []int) (string, bool) {
	if unicode.IsSpace(runeBefore(s, loc[0])) && unicode.IsSpace(runeAfter(s, loc[1])) {
		return "and", true
	}
	return "", false
}

func spNumberSign(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) {
		return "", false
	}
	return "number " + spGroup(s, loc, 1), true
}

// spForm spells the early-years form names so the voice reads them letter by letter.
func spForm(s string, loc []int) (string, bool) {
	if !okLeft(s, loc[0]) || !okRight(s, loc[1]) {
		return "", false
	}
	return "F S " + spGroup(s, loc, 1), true
}
