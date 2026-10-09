package speech

import "testing"

type spokenCase struct{ in, want string }

func checkSpoken(t *testing.T, cases []spokenCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := Spoken(tc.in); got != tc.want {
				t.Errorf("Spoken(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSpokenCurrency(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"AED 51,917", "fifty-one thousand nine hundred and seventeen dirhams"},
		{"AED51,917", "fifty-one thousand nine hundred and seventeen dirhams"},
		{"51,917 AED", "fifty-one thousand nine hundred and seventeen dirhams"},
		{"£1,200", "one thousand two hundred pounds"},
		{"AED 12.50", "twelve dirhams fifty"},
		{"AED 12.00", "twelve dirhams"},
		{"AED 0.50", "zero dirhams fifty"},
		{"AED 1", "one dirham"},
		{"1 dirham", "one dirham"},
		{"AED 400–1,500", "four hundred to one thousand five hundred dirhams"},
		{"AED 400-1,500", "four hundred to one thousand five hundred dirhams"},
		{"Dhs 250", "two hundred and fifty dirhams"},
		{"Dh 5", "five dirhams"},
		{"$99", "ninety-nine dollars"},
		{"US$ 1,000,000", "one million dollars"},
		{"USD 20", "twenty dollars"},
		{"€45.05", "forty-five euros oh five"},
		{"GBP 2,500", "two thousand five hundred pounds"},
		{"EUR 1,200.75", "one thousand two hundred euros seventy-five"},
		{"AED 1.5 million", "one point five million dirhams"},
		{"1,200 dirhams", "one thousand two hundred dirhams"},
		{"AED 1,005", "one thousand and five dirhams"},
		{"AED 100/month", "one hundred dirhams/month"},
		{"The fee is AED 41,200 per term.", "The fee is forty-one thousand two hundred dirhams per term."},
		{"Fee: $99", "Fee: ninety-nine dollars"},
	})
}

func TestSpokenPlainNumbers(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"1,300 pupils", "one thousand three hundred pupils"},
		{"12345 pupils", "twelve thousand three hundred and forty-five pupils"},
		{"1,000,000 visits", "one million visits"},
		{"2,500,000,000", "two billion five hundred million"},
		{"10,000", "ten thousand"},
		{"1,105", "one thousand one hundred and five"},
		{"20,000 and 1,250 places", "twenty thousand and one thousand two hundred and fifty places"},
		{"Year 7 has 1,300 pupils in FS2", "Year 7 has one thousand three hundred pupils in F S 2"},
	})
}

func TestSpokenTimes(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"09:00", "9 a.m."},
		{"14:30", "2:30 p.m."},
		{"12:00", "12 noon"},
		{"00:00", "midnight"},
		{"00:30", "12:30 a.m."},
		{"12:30", "12:30 p.m."},
		{"12:00 am", "midnight"},
		{"12:00 pm", "12 noon"},
		{"9:30 am", "9:30 a.m."},
		{"9.30am", "9:30 a.m."},
		{"9:30AM", "9:30 a.m."},
		{"9:30 a.m.", "9:30 a.m."},
		{"9am", "9 a.m."},
		{"5 pm", "5 p.m."},
		{"1:30 pm", "1:30 p.m."},
		{"7:30–15:30", "7:30 a.m. to 3:30 p.m."},
		{"8:00-12:00", "8 a.m. to 12 noon"},
		{"9:30–11:30 am", "9:30 to 11:30 a.m."},
		{"at 09:00.", "at 9 a.m."},
		{"Open from 08:00 to 16:00.", "Open from 8 a.m. to 4 p.m."},
		{"Tours run 09:00–15:30, Sunday to Thursday.", "Tours run 9 a.m. to 3:30 p.m., Sunday to Thursday."},
	})
}

func TestSpokenDates(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"2026-10-19", "19 October 2026"},
		{"on 2026-01-05.", "on 5 January 2026."},
		{"19/10/2026", "19 October 2026"},
		{"1/2/2026", "1 February 2026"},
		{"Open day: 2026-10-19 at 09:00.", "Open day: 19 October 2026 at 9 a.m."},
	})
}

func TestSpokenNumberRanges(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"20–24", "20 to 24"},
		{"20-24 pupils", "20 to 24 pupils"},
		{"Year 7-9", "Year 7 to 9"},
		{"ages 5 – 11", "ages 5 to 11"},
		{"1,300-1,500 pupils", "one thousand three hundred to one thousand five hundred pupils"},
	})
}

func TestSpokenPercent(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"7%", "7 percent"},
		{"100%", "100 percent"},
		{"7.5 %", "7.5 percent"},
		{"20-24%", "20 to 24 percent"},
		{"1,200%", "one thousand two hundred percent"},
		{"Up to 15% off", "Up to 15 percent off"},
	})
}

func TestSpokenPhones(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"+971 4 586 2700", "plus nine seven one, four, five eight six, two seven zero zero"},
		{"+971 (0) 4 586 2700", "plus nine seven one, four, five eight six, two seven zero zero"},
		{"04 586 2700", "zero four, five eight six, two seven zero zero"},
		{"Call +971 4 586 2700 today.", "Call plus nine seven one, four, five eight six, two seven zero zero today."},
	})
}

func TestSpokenEmails(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"info@theaquilaschool.com", "info at theaquilaschool dot com"},
		{"Email admissions.office@theaquilaschool.com.", "Email admissions dot office at theaquilaschool dot com."},
		{"j_smith-1@school.ae", "j underscore smith dash 1 at school dot ae"},
		{"Call +971 4 586 2700 or email info@theaquilaschool.com.", "Call plus nine seven one, four, five eight six, two seven zero zero or email info at theaquilaschool dot com."},
	})
}

func TestSpokenAbbreviations(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"e.g. lunch", "for example lunch"},
		{"Fees, e.g., trips", "Fees, for example, trips"},
		{"E.g. this", "For example this"},
		{"i.e. Monday", "that is Monday"},
		{"approx. 20 pupils", "approximately 20 pupils"},
		{"Smith & Sons", "Smith and Sons"},
		{"A vs. B", "A versus B"},
		{"A vs B", "A versus B"},
		{"No. 5", "number 5"},
		{"FS1 and FS2", "F S 1 and F S 2"},
		{"Fees are AED 51,917 per year (approx. 14,000 USD).", "Fees are fifty-one thousand nine hundred and seventeen dirhams per year (approximately fourteen thousand dollars)."},
	})
}

func TestSpokenKeepsVocalEvents(t *testing.T) {
	checkSpoken(t, []spokenCase{
		{"(laugh)", "(laugh)"},
		{"(sigh)", "(sigh)"},
		{"(cough)", "(cough)"},
		{"(clears throat) Hello", "(clears throat) Hello"},
		{"(Laugh)", "(Laugh)"},
		{"(laugh) AED 5 (sigh)", "(laugh) five dirhams (sigh)"},
	})
}

func TestSpokenLeavesUncertainTextAlone(t *testing.T) {
	for _, in := range []string{
		"",
		"Hello, how can I help?",
		"Year 7",
		"Founded in 2026",
		"In 1998 there were 105 pupils",
		"Room 12B",
		"L001",
		"TAQS-0001",
		"TAQS-12345",
		"TAQS-FS1",
		"info12345",
		"version 1.2.3",
		"R&D and Q&A",
		"approximately 20",
		"No thanks",
		"No. Thanks",
		"1:30",
		"24:00",
		"1.30",
		"A$100",
		"AED 5k",
		"5-3",
		"586-2700",
		"+20 pupils",
		"ID 0012345",
		"1,300.5 km",
		"2026-13-45",
		"31/02/2026",
	} {
		t.Run(in, func(t *testing.T) {
			if got := Spoken(in); got != in {
				t.Errorf("Spoken(%q) = %q, want it unchanged", in, got)
			}
		})
	}
}

func TestSpokenIsIdempotent(t *testing.T) {
	for _, in := range []string{
		"19 October 2026",
		"9 a.m.",
		"2:30 p.m.",
		"20 to 24",
		"7 percent",
		"fifty-one thousand nine hundred and seventeen dirhams",
		"plus nine seven one, four, five eight six, two seven zero zero",
		"info at theaquilaschool dot com",
	} {
		if got := Spoken(in); got != in {
			t.Errorf("Spoken(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestSpokenIntWords(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "zero"},
		{5, "five"},
		{13, "thirteen"},
		{21, "twenty-one"},
		{100, "one hundred"},
		{105, "one hundred and five"},
		{999, "nine hundred and ninety-nine"},
		{1000, "one thousand"},
		{1001, "one thousand and one"},
		{1100, "one thousand one hundred"},
		{12345, "twelve thousand three hundred and forty-five"},
		{100000, "one hundred thousand"},
		{1000000, "one million"},
		{1000001, "one million and one"},
		{123456789, "one hundred and twenty-three million four hundred and fifty-six thousand seven hundred and eighty-nine"},
		{2500000000, "two billion five hundred million"},
		{1000000000000, "one trillion"},
	} {
		if got := spIntWords(tc.n); got != tc.want {
			t.Errorf("spIntWords(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}
