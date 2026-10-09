# The Aquila School demo

A sales demo for The Aquila School, Dubai (British curriculum, ages 3 to 18, Outstanding by BSO twice). Three personas answer in the school's own voice, simulate the back-office systems behind every task, and share memory about each family. The research behind the content is in `docs/research/aquila-school-profile.md`; the distilled version each persona reads is `agents/aquila-*/knowledge/school-profile.md`.

All figures are the real 2026-27 fees and discounts. Anything the school has not published (deposit, vacancies, tour times, club costs) uses the "demo assumptions" table, stated confidently. Every screen says "Demo simulation. Data is illustrative."

## The personas

| Persona                      | Role                       | Direction | Opens with                                                                                           |
| ---------------------------- | -------------------------- | --------- | ---------------------------------------------------------------------------------------------------- |
| Amelia (`aquila-admissions`) | Admissions advisor         | inbound   | greets the caller by name and picks up where the last conversation ended                             |
| Noor (`aquila-reception`)    | Reception and front office | inbound   | answers the front desk, greets known parents, asks after the child                                   |
| Sophie (`aquila-outreach`)   | Admissions outreach        | outbound  | calls the contact, introduces herself, asks if now is a good time and references why they went quiet |

All three use `industry: aquila` and `memory.domain: aquila-school`, so one family has one shared memory across the three agents. The agent speaks first: inbound personas open automatically when a session starts, and for Sophie the UI shows a "Place call to ..." button. The same opening works on SIP calls.

## The contacts

Pick the contact in the identity dropdown. The history comes from the mock CRM (`crm.history`) and the seeded memories.

| ID   | Contact                             | Pick for                                          | What is seeded                                                                                                                                                                                                                                                                    |
| ---- | ----------------------------------- | ------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| L001 | Sarah Ahmed (lost lead)             | Sophie: the classic win-back. Amelia: a fee quote | Toured 14 May with Mia (FS2, age 4), loved the urban farm and play-based EYFS. Went quiet after the fee talk (AED 51,917), comparing a school in Arabian Ranches. Adam (2) joins FS1 in 2027, so the sibling discount, early bird and Family Circle referral were never discussed |
| L002 | Raj and Priya Menon (lost lead)     | Sophie: assessment and Head of Secondary          | Year 7 for Arjun (11), robotics and STEAM fan. Application started 2 June, CAT4 never booked, worried about moving from an Indian curriculum and about Arabic                                                                                                                     |
| L003 | Olivia Brown (lost lead)            | Sophie: relocation and buses                      | Twins Ella and Jack (Year 4) arriving from London in January 2027. Virtual tour 3 July, worried about Arabic and the JVC bus (zone 3), silent since August                                                                                                                        |
| L004 | Hassan Khoury (active enquiry)      | Amelia: inclusion and Hemam                       | Karim (8, Year 3) has dyslexia. Spoke to Amelia on 20 September; a meeting with Claire Hitchings, Head of Inclusion, was promised and never booked. Prefers phone calls                                                                                                           |
| F001 | Fatima Al Mansoori (current parent) | Noor: absence and clubs                           | Omar (Year 5) and Layla (Year 2), Arabian Ranches bus. Layla was reported absent with a fever on 2 October; Fatima asked about Arabic enrichment clubs                                                                                                                            |
| F002 | Michael Chen (current parent)       | Noor or Amelia: Sixth Form                        | Emily (Year 10). Asked about IBDP versus IBCP, the Sixth Form Options Evening (29 October) and the Tomorrow's Leaders scholarship                                                                                                                                                 |
| F003 | Aisha Rahman (new enquiry)          | Amelia: first contact                             | No history at all. Shows how a brand-new family is welcomed                                                                                                                                                                                                                       |

## Suggested flows

### 1. Win back a lost lead (Sophie, L001 Sarah Ahmed)

1. Choose Sophie, then Sarah Ahmed, and press "Place call to Sarah Ahmed". Sophie introduces herself, mentions the May tour and Mia, and asks if it is a good time.
2. Say "Yes, go ahead." She references the fees and the other school. Ask: "What would two children cost?" Sophie calls `admissions.fee_quote` for FS2 and FS1 with sibling, early-bird and Family Circle applied: FS2 AED 51,917 and FS1 AED 48,673, down to AED 37,899 and AED 33,098 when the full year is paid up front through the referral (AED 70,997 against AED 100,590).
3. Say "Could we visit on a Saturday?" She offers the real open morning and tour slots; confirm one on the card.
4. End the call. Sophie logs the outcome (`outreach.log_outcome`), and the contact's status changes to re-engaged.

Shows: outbound call, memory and CRM history, simulated booking with confirmation, the fee engine, outcome logging.

### 2. Memory across agents (Noor then Amelia, F001 Fatima)

1. Open Noor as Fatima. Noor greets her by name and asks after Layla. Say "Layla has a fever again today, she will be off." Confirm the absence card; Noor records it and mentions the Arabic enrichment club.
2. Say "Omar loves football, he wants to try out for the Year 5 team." Noor saves a note.
3. Switch to Amelia with the same contact. She greets Fatima, mentions Layla's absence and Omar's football without being told again.

Shows: persistent memory shared by two personas, `crm.note`, `reception.report_absence`, clubs and buses.

### 3. Inclusion, a promised meeting (Amelia, L004 Hassan Khoury)

1. Open Amelia as Hassan. She greets him, mentions Karim and the Hemam conversation on 20 September and the meeting that was promised.
2. Say "Yes, please set up that meeting." She books Claire Hitchings via `staff.meeting_book` (confirm the card) with a time.
3. Ask "What does Hemam offer?" The answer is warm and complete (ABA, speech and language, occupational therapy, SENDIA award).

Shows: following up a promise the school made, inclusion tone, simulated meeting booking.

### 4. First contact and a simulated application (Amelia, F003 Aisha Rahman)

1. Open Amelia as Aisha. No history: a warm welcome and a question about the child.
2. Say "I have a four year old daughter, Zara, and we live in Dubailand." Amelia saves a note, answers about FS2, class sizes and the waiting-list position, and offers a tour.
3. Ask about fees, a tour or "start an application for Zara in FS2": quote, slot booking and `application.start`.
4. Start a new session as Aisha: the new conversation remembers the child and the tour.

Shows: first-contact experience, `admissions.availability`, application start, memory between sessions.

### 5. Assessment and transition (Sophie or Amelia, L002 Menon)

Ask "Can Arjun cope with the move from an Indian curriculum?" The agent answers confidently, offers a meeting with Yasmine Dannawy and the CAT4 slots, and books one. The application status then reads "assessment booked" (`application.status`).

### 6. Relocation, buses and Arabic (Sophie, L003 Olivia Brown)

Ask "What would the bus from JVC cost?" (`transport.quote`: zone 3, AED 9,588 a year, paid termly) and "Will the twins catch up on Arabic?" Then book a virtual tour.

### 7. Sixth Form decision (Noor or Amelia, F002 Michael Chen)

Ask about the IBDP, IBCP and the Tomorrow's Leaders scholarship (`scholarship.check`), book Yasmine Dannawy, and note the 29 October options evening.

## What each flow demonstrates

| Capability                        | Where to see it                                                                                                                         |
| --------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Persistent memory across sessions | Run any flow twice with the same contact; the second session recalls the first                                                          |
| Memory across agents              | Flow 2: Noor writes, Amelia remembers. Sophie also reads what the other two recorded                                                    |
| Simulated booking                 | Tours, CAT4 and meet-and-greet, meetings, callbacks and bus seats, each shown on a confirmation card and visible in later `crm.history` |
| Fee quote                         | Flow 1: real 2026-27 fees with sibling, early-bird and Family Circle discounts, termly instalments and deposit                          |
| Outreach call                     | Flow 1, 5 and 6: the agent speaks first, asks permission, references the past and logs the outcome                                      |
| Tone and no dead ends             | Ask anything unusual; `school.information` always returns a positive, substantive answer                                                |

## Operations notes

- Provision NovaMem identities once before the first deploy: `python3 deploy/kw/provision-memory.py --scopes-file deploy/kw/aquila-scopes.json`. Seeds for an unprovisioned scope abort startup.
- Seeds live only in `agents/aquila-admissions/memory-seeds.json` so the shared scope is seeded once.
- Slots are generated relative to startup for the next 14 days in Dubai time (weekday 9:00, 10:00 and 11:00 tours, virtual tours at 14:00 and 15:00, CAT4 and meet-and-greet slots, and a Saturday open morning), skipping 2026-27 school closures. Mock records reset on every rollout.
- Locally the default is the offline scripted mode: the opening is a fixed greeting and the replies call tools from `demo.json`. Connect a real LLM (`LLM_PROVIDER=openai-compatible`) for the personalised conversations described above.
- `GET /api/agents` returns `persona.opening` (`inbound` or `outbound`). `POST /api/sessions/{id}/open` starts the opening turn; see `docs/api.md`.
