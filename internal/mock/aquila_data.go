package mock

import "enterprise-ai-demo/internal/tools"

// Static Aquila School content, distilled from docs/research/aquila-school-profile.md.
// Values marked "demo assumption" there are stated confidently by the personas.

type aquilaTopic struct {
	keys  []string
	title string
	text  string
}

var aquilaOverview = aquilaTopic{
	title: "The Aquila School at a glance",
	text:  "The Aquila School is a community-focused British international school in Dubai (Wadi Al Safa 5, Dubailand Residential Complex, near Al Ain Road and Emirates Road) for pupils aged 3 to 18, from FS1 to Year 13. It opened in 2018, is the flagship of International Schools Partnership (121 schools in 25 countries) and now has over 1,300 pupils from over 90 nationalities. Rated Outstanding by BSO, twice, and Good by KHDA, with Foundation Stage rated Outstanding. Values: Integrity, Perseverance, Resilience, Kindness. Safe, happy learning. Phone +971 4 586 2700, info@theaquilaschool.com.",
}

var aquilaTopics = []aquilaTopic{
	{
		keys:  []string{"curriculum", "british", "academic", "subjects", "gcse", "results", "teaching", "how we learn", "good struggles"},
		title: "Curriculum and results",
		text:  "A blended British curriculum adapted to the UAE, with Arabic for everyone, Islamic Studies and Moral, Social and Cultural Studies. Primary uses the English National Curriculum with Spanish from Year 3, swimming and music. Secondary leads to GCSE, BTEC Level 2 and LAMDA; Post-16 offers IBDP, IBCP, BTEC Level 3 and ASDAN. Results 2026: GCSE and IGCSE 100% pass with 87% at grades 9 to 4 and 31% at 9 to 7; IBDP 100% pass with an average of 34.5 and a top score of 39; IBCP and LAMDA 100% pass. Learning is built on ISP Learning.First, the power of good struggles and teachers as co-pilots.",
	},
	{
		keys:  []string{"language", "arabic", "spanish", "french", "mandarin", "german", "home language", "mother tongue"},
		title: "Languages and Arabic",
		text:  "Arabic is taught from Early Years to Post-16, with Spanish from Year 3 and IB French, German and Mandarin. A home-language programme (French, German, Spanish, Russian) supports qualifications in over 30 languages. Pupils who are new to Arabic are supported with small-group catch-up so they settle in confidently.",
	},
	{
		keys:  []string{"process", "apply", "application", "document", "enrol", "how to join", "deposit", "registration", "waiting list", "vacanc", "places", "age", "admission"},
		title: "Admissions process",
		text:  "Five gentle steps: enquiry, a school tour (virtual for overseas families), a meet and greet (with the Head of Primary or a phase leader for FS1 to Year 6; a CAT4 and an interview with the Head of Secondary for Secondary), an offer by email accepted within 5 working days, then registration and a deposit of AED 5,000 credited against Term 1 fees. A welcome visit follows. There is no application fee and applications are accepted all year. Documents: passports, visas and Emirates IDs for the child and parents, vaccination record, attested transfer certificate and registrar forms. Age is counted at 31 August. Sibling priority applies. Most year groups have places; there are small waiting lists in FS2, Year 1 and some Year 10 option blocks, and a waiting-list offer is held for one week.",
	},
	{
		keys:  []string{"fee", "tuition", "cost", "price", "afford", "payment", "instalment", "installment", "term fees"},
		title: "Tuition fees 2026-27",
		text:  "KHDA-approved annual tuition in AED: FS1 48,673; FS2 51,917; Years 1 to 2 54,081; Years 3 to 4 56,244; Years 5 to 6 59,489; Years 7 to 8 64,897; Years 9 to 11 71,386; Years 12 to 13 77,876. Paid 40/30/30 by term. Excludes transport, uniform and optional clubs. Use the fee quote for an exact figure with discounts.",
	},
	{
		keys:  []string{"discount", "sibling", "referral", "family circle", "early bird", "early-bird", "corporate", "offer", "save", "saving"},
		title: "Discounts and the Family Circle",
		text:  "Sibling discounts: 5% for the second child, 15% for the third, 25% for the fourth and 100% for the fifth. Early bird for paying the full year: 7% by 31 May, 5% by 30 June, 3% by 31 July. The Aquila Family Circle referral runs 27 August to 31 October 2026: the new family receives 20% off 2026-27 tuition and the referring family receives AED 5,000, then AED 2,000 for each further referral. Corporate partners include Visa, Fazaa, Emirates, Esaad and HSBC (not combinable with sibling discounts).",
	},
	{
		keys:  []string{"scholar", "bursary", "financial aid"},
		title: "Scholarships",
		text:  "ISP Middle East offers 100 scholarships worth up to 100% of tuition. Academic Excellence and International Learner awards are for Years 7 to 10 and Year 12; Tomorrow's Leaders is for Year 12 with five or more GCSEs at grade 6 or above. Performing arts scholarships go up to 75%. Requirements: 95% attendance, two extra-curricular activities and a 300 to 500 word essay, followed by assessment by the senior leadership team and then the Principal. Applications open in spring and are decided by June, with awards from 25% to 100%.",
	},
	{
		keys:  []string{"inclusion", "hemam", "special educational", "dyslexia", "learning support", "autism", "adhd", "determination", "speech", "therapy", "aba", "occupational"},
		title: "Inclusion and Hemam Learning Support",
		text:  "Everyone deserves to be seen, heard and empowered to thrive. The Hemam Learning Support Centre offers ABA, speech and language therapy and occupational therapy alongside in-class support, and the school holds the SENDIA award. Claire Hitchings, Head of Inclusion, meets families to build a personal plan, and each pupil with additional needs has a clear support plan reviewed with parents.",
	},
	{
		keys:  []string{"campus", "facilit", "pool", "farm", "lab", "theatre", "music", "security", "nurse", "medical", "cafe", "pitch", "gym", "hydroponic", "building"},
		title: "Campus and facilities",
		text:  "A purpose-built campus: science and STEM labs with a drone arena, pools, an auditorium and black-box theatre, two music studios, a sprung-floor dance studio, an astro pitch, gym and courts, a food tech room, an urban farm with hydroponics, greenhouses and a vertical UV garden, prayer rooms, the Parrot Cafe and the Senior's Cafe. Safety first: 24/7 security and CCTV and on-site nurses and a doctor.",
	},
	{
		keys:  []string{"steam", "stem", "robot", "drone", "coding", "computing", "artificial intelligence", "technology", "first lego"},
		title: "STEAM, robotics and AI",
		text:  "Strong STEAM: robotics and drones (Python, Lego Spike, Tello), First Lego League, computing and an urban farm that doubles as a living science lab. AI is used with guidance, there are no phones during the school day and mistakes are treated as part of learning. A device is required in Secondary.",
	},
	{
		keys:  []string{"timing", "hours", "start time", "finish", "school day", "drop-off", "drop off", "pick up", "pick-up", "office", "opening hours", "term date", "calendar", "holiday", "half term", "break", "eid"},
		title: "School day, office hours and calendar",
		text:  "Early Years finishes at 1:45 pm, Primary at 2:45 pm and Secondary and Post-16 at 3:30 pm, Monday to Thursday; everyone finishes at 11:40 am on Friday. Drop-off is 7:25 to 7:40. The office is open Monday to Thursday 7:30 to 15:30 and Friday 7:30 to 11:40. 2026-27: Term 1 is 1 September to 11 December (half term 12 to 16 October, National Day 2 to 4 December); Term 2 is 4 January to 2 April (Eid 8 to 12 March); Term 3 is 12 April to 2 July (Eid 17 to 18 May).",
	},
	{
		keys:  []string{"bus", "transport", "route", "commute"},
		title: "School buses",
		text:  "School-run buses are priced by zone and charged annually, paid termly: AED 6,877 (Dubailand, Falcon City, DSO, Academic City); AED 8,927 (Arabian Ranches, Motor City, Mudon, Mirdif); AED 9,588 (Damac Hills 2, JVC). Contact bus@theaquilaschool.com.",
	},
	{
		keys:  []string{"uniform", "trutex", "polo", "shirt"},
		title: "Uniform",
		text:  "Uniform comes from Trutex (trutex.ae, school code TAQS-0001): an Aquila blue polo for EYFS and Primary, a striped shirt for Secondary and a black collared T-shirt for Post-16. A full set is about AED 600 to 900.",
	},
	{
		keys:  []string{"club", "activity", "activities", "sport", "co-curricular", "after school", "duke", "dofe", "mun", "tedx", "wrap"},
		title: "Clubs, sports and wrap-around care",
		text:  "Clubs cover arts, Lego, computing, mindfulness and languages, plus Aquila All Stars sports teams, Duke of Edinburgh (Bronze, Silver, Gold), ISP Model United Nations, TEDx and First Lego League, with trips from Year 7. Breakfast club runs 6:30 to 7:30, secondary swimming 6:40 to 7:20 and Early Years wrap-around care 3:35 to 4:35. Many clubs are free; specialist clubs and wrap-around care cost AED 400 to 1,500 per term.",
	},
	{
		keys:  []string{"leader", "principal", "head of", "team", "staff", "headteacher", "who runs"},
		title: "Leadership team",
		text:  "Wayne Howsen, Principal; Kylie Cleworth, Head of Primary; Yasmine Dannawy, Head of Secondary; Claire Hitchings, Head of Inclusion; Dr Mahmoud Khalil, Head of Arabic and Islamic; Pauline Lamond, Safeguarding Lead. Class sizes are about 20 to 24 pupils, with a teaching assistant in every Foundation Stage class.",
	},
	{
		keys:  []string{"rating", "outstanding", "bso", "khda", "inspection", "accredit", "award", "ofsted", "ib world"},
		title: "Ratings and accreditations",
		text:  "Rated Outstanding by BSO in all areas, in 2022 and again in 2025, and Good by KHDA, with Foundation Stage rated Outstanding in 2024. An IB World School (2024), holder of the SENDIA award and the National Mental Health and Wellbeing accreditation, a Platinum Best School to Work For (2025) and a Guinness World Record holder (2021) for a history lesson uniting 73 nationalities.",
	},
	{
		keys:  []string{"contact", "phone", "email", "address", "location", "where", "direction", "parking", "find", "map"},
		title: "Contact and location",
		text:  "The Aquila School, Wadi Al Safa 5, Dubailand Residential Complex (DLRC), Dubai, near the junction of Al Ain Road and Emirates Road. Phone +971 4 586 2700; info@theaquilaschool.com; buses bus@theaquilaschool.com. Visitors check in at main reception.",
	},
	{
		keys:  []string{"sixth form", "post-16", "post 16", "ibdp", "ibcp", "btec", "year 12", "year 13", "university", "ucas", "careers", "options evening"},
		title: "Sixth Form and Post-16",
		text:  "Four pathways to graduation: IBDP (with TOK, Extended Essay and CAS), IBCP, BTEC Level 3 and ASDAN. IBDP typically needs about five GCSEs at grade 5 or above with 6s in Higher Level subjects; IBCP and BTEC are more flexible. A full-time careers adviser, a UCAS centre, UniFrog and work placements from Year 12 support every student. The Sixth Form Options Evening for Year 11 is on 29 October 2026.",
	},
	{
		keys:  []string{"early years", "eyfs", "fs1", "fs2", "foundation", "class size", "nursery", "reception", "play"},
		title: "Early Years (FS1 and FS2)",
		text:  "Play-based learning in the Early Years Foundation Stage, rated Outstanding in 2024: FS1 is for children turning 4 and FS2 for those turning 5 in the school year (age counted at 31 August). Classes are about 20 to 24 children with a teaching assistant in every class, a hydroponic farm and gardens to explore, and wrap-around care from 3:35 to 4:35.",
	},
	{
		keys:  []string{"event", "open evening", "open morning", "picnic", "coming up", "news", "what's on", "whats on"},
		title: "Coming up at Aquila",
		text:  "Sixth Form Options Evening for Year 11 (29 October 2026), GCSE Options Evening for Year 9, the Teddy Bears' Picnic (15 November 2026) and a monthly Saturday open morning from 10:00 to 12:00. The Family Circle referral offer runs until 31 October 2026. The community also raised AED 170,000 with Dubai Cares to build a school in Nepal, which opened in 2025 with 219 pupils.",
	},
	{
		keys:  []string{"safe", "wellbeing", "safeguard", "mental", "bullying", "pastoral", "happy"},
		title: "Safeguarding and wellbeing",
		text:  "Safe, happy learning comes first. Pauline Lamond is the Safeguarding Lead, the campus has 24/7 security, CCTV and on-site nurses and a doctor, and the school holds the National Mental Health and Wellbeing accreditation. Pupils belong to a close-knit community in which differences are strengths.",
	},
}

type aquilaClub struct {
	name, phase, cost, text string
}

var aquilaClubs = []aquilaClub{
	{"Arabic Enrichment Club", "Primary", "Free", "Arabic conversation, storytelling and calligraphy that builds on the school's Arabic programme; popular with pupils of every background."},
	{"Spanish Conversation Club", "Primary and Secondary", "Free", "Relaxed Spanish conversation, songs and games, a friendly boost for pupils who begin Spanish in Year 3."},
	{"Lego Club", "Primary", "Free", "Free-build and challenge sessions with Lego Spike, a favourite with budding engineers."},
	{"Computing and Coding Club", "Primary and Secondary", "Free", "Python, game design and drones in the drone arena."},
	{"First Lego League Robotics", "Primary and Secondary", "Specialist, AED 400 to 1,500 per term", "Competition robotics team that builds, programmes and presents a robot; coached by STEAM staff."},
	{"Mindfulness Club", "All phases", "Free", "Calm, breathing and gratitude sessions that support wellbeing."},
	{"Arts and Crafts Club", "Primary", "Free", "Painting, clay and mixed media in the art studios."},
	{"Aquila All Stars Sports", "All phases", "Free", "School sports teams and fixtures on the astro pitch, courts and in the pools."},
	{"Secondary Swimming", "Secondary", "Specialist, AED 400 to 1,500 per term", "Early-morning training in the school pool, 6:40 to 7:20."},
	{"Performing Arts and LAMDA", "Primary and Secondary", "Specialist, AED 400 to 1,500 per term", "Drama, dance and music with LAMDA qualifications; performances in the auditorium and black-box theatre."},
	{"Duke of Edinburgh's Award", "Secondary", "Specialist, AED 400 to 1,500 per term", "Bronze, Silver and Gold expeditions and service."},
	{"Model United Nations and TEDx", "Secondary", "Free", "ISP Model United Nations debating and TEDx talks to build confident public speakers."},
	{"Breakfast Club", "All phases", "Wrap-around, AED 400 to 1,500 per term", "A calm, supervised start from 6:30 to 7:30."},
	{"Early Years Wrap-around Care", "FS1 and FS2", "Wrap-around, AED 400 to 1,500 per term", "Supervised play and a snack from 3:35 to 4:35."},
}

type aquilaChild struct {
	user, name, year, detail string
	age                      float64
	status                   string
}

var aquilaChildren = []aquilaChild{
	{"F001", "Omar", "Year 5", "Loves football and is hoping for the Year 5 team.", 9, "current pupil"},
	{"F001", "Layla", "Year 2", "Bright and chatty; absent on 2 October with a fever.", 6, "current pupil"},
	{"F002", "Emily", "Year 10", "Thinking about Sixth Form pathways; interested in Computer Science and Business.", 14, "current pupil"},
	{"L001", "Mia", "FS2", "Applying for an FS2 place; loved the urban farm.", 4, "prospective pupil"},
	{"L001", "Adam", "FS1", "Younger brother, expected to join FS1 in 2027.", 2, "future applicant"},
	{"L002", "Arjun", "Year 7", "Keen on robotics and STEAM; currently in an Indian curriculum school.", 11, "prospective pupil"},
	{"L003", "Ella", "Year 4", "Twin; relocating from London in January 2027.", 8, "prospective pupil"},
	{"L003", "Jack", "Year 4", "Twin; relocating from London in January 2027.", 8, "prospective pupil"},
	{"L004", "Karim", "Year 3", "Has dyslexia; the family wants Hemam and in-class support.", 8, "prospective pupil"},
}

type aquilaInteraction struct {
	user, date, channel, who, text string
}

var aquilaInteractions = []aquilaInteraction{
	{"F001", "2026-09-02", "Reception call", "Noor", "Confirmed the Arabian Ranches bus pick-up for Omar and Layla; settled well into the new term."},
	{"F001", "2026-10-02", "Reception call", "Noor", "Reported Layla (Year 2) absent with a fever and asked about Arabic enrichment clubs for the children."},
	{"F002", "2026-09-10", "Reception call", "Noor", "Asked about Year 10 option blocks and Emily's progress."},
	{"F002", "2026-09-25", "Admissions call", "Amelia", "Asked about Sixth Form, IBDP versus IBCP, the Sixth Form Options Evening on 29 October and the Tomorrow's Leaders scholarship."},
	{"L001", "2026-04-22", "Web enquiry", "Amelia", "Enquired about an FS2 place for Mia (4) and mentioned Adam (2) for FS1 in 2027."},
	{"L001", "2026-05-14", "Campus tour", "Amelia", "Toured with Mia. Loved the urban farm, hydroponics and the play-based Early Years; asked about class sizes and settling in."},
	{"L001", "2026-05-20", "Admissions call", "Amelia", "Discussed FS2 fees of AED 51,917. Sarah found the total high next to a school in Arabian Ranches and asked for time to think. Sibling discount, early bird and referral not yet discussed."},
	{"L001", "2026-06-10", "Follow-up email", "Amelia", "No reply; lead went quiet."},
	{"L002", "2026-05-27", "Enquiry call", "Amelia", "Enquired about Year 7 for Arjun (11), keen on robotics and First Lego League; currently at an Indian curriculum school."},
	{"L002", "2026-06-02", "Online application", "Amelia", "Started an application for Arjun (Year 7). Documents and the CAT4 are outstanding."},
	{"L002", "2026-06-09", "Admissions email", "Amelia", "Sent CAT4 slot options and a transition guide; no reply."},
	{"L002", "2026-06-23", "Admissions call", "Amelia", "Priya worried about the move from an Indian curriculum to British GCSE and about Arabic. A conversation with Yasmine Dannawy, Head of Secondary, was suggested but not scheduled."},
	{"L003", "2026-06-18", "Web enquiry", "Amelia", "Olivia enquired from London about two Year 4 places for twins Ella and Jack from January 2027."},
	{"L003", "2026-07-03", "Virtual tour", "Amelia", "Video tour. Loved the multicultural community; worried about catching up on Arabic and asked about a JVC bus."},
	{"L003", "2026-07-10", "Admissions email", "Amelia", "Sent a fee overview (Years 3 to 4: AED 56,244 each) and Arabic support information. Olivia said she would confirm once her visa and move were settled."},
	{"L003", "2026-08-12", "Chase email", "Amelia", "No reply since."},
	{"L004", "2026-09-14", "Web enquiry", "Amelia", "Hassan enquired about Year 3 for Karim (8), who has dyslexia, and asked about learning support."},
	{"L004", "2026-09-20", "Admissions call", "Amelia", "Long call about Hemam Learning Support. Hassan wants Karim to be understood, not labelled. Promised a meeting with Claire Hitchings, Head of Inclusion; not yet scheduled."},
	{"L004", "2026-09-28", "Admissions email", "Amelia", "Hassan asked whether Claire could review Karim's educational psychology report at that meeting; yes."},
}

func aquilaHistoryRecords() []tools.Record {
	out := []tools.Record{}
	counts := map[string]int{}
	for _, h := range aquilaInteractions {
		counts[h.user]++
		out = append(out, tools.Record{ID: "INT-" + h.user + "-" + string(rune('0'+counts[h.user])), Kind: "interaction", UserID: h.user, Name: h.date + " " + h.channel, Specialty: h.who, Description: h.text, Status: "completed"})
	}
	return out
}
