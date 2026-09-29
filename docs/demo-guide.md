# Sales demo walkthrough

The KW deployment uses Qwen3.5-9B and live NovaMem. For a local scripted walkthrough, use the default offline provider. Each scripted interaction invokes the same HTTP tools that a live model uses. Tool outputs and actual state changes are visible in the activity panel. A live model enables broader phrasing; offline action requests use the IDs shown in the conversation.

## Telecom

Select **Telecom Support**, the indicated customer, and **Start session**.

| Scenario | Identity | Prompt | Expected result |
|---|---|---|---|
| T1 | C001 Pascal Martin | My internet upstairs is terrible again. | Retrieves prior interference and troubleshooting preference; checks outage, line, and Wi-Fi. Follow with “Optimize my Wi-Fi channel” and confirm. |
| T2 | C002 Leila Haddad | My internet is down. | Regional outage; equipment troubleshooting is skipped. “Restart my router” does not offer a restart. |
| T3 | C003 Noah Brooks | My internet is unavailable. | Restricted service and outstanding balance. “Explain my bill” shows the overdue invoice. |
| T4 | C004 Aisha Khan | Explain my bill. | $105 total versus $50 previously: $35 roaming and $20 installation. |
| T5 | C005 Ethan Cole | My data allowance is exhausted. | 20/20 GB used, throttling, usage history, actual plan catalog. |
| T6 | C006 Sofia Reyes | Restart my router. | Checks access status, offers confirmation, then repairs router health. |
| T7 | C007 Omar Farouk | My internet is broken. | Physical line fault and available technician slots. “Book technician TECH-01” and confirm. |
| T8 | C008 Maya Chen | Compare available plans. | Current subscription, usage, and actual prices. “Change plan to fiber-500” and confirm. |

Other offline examples: “Show my account”, “Create ticket for recurring upstairs coverage”, “Show tickets”, “Close ticket T-C001”. Every configured tool is available to a live model, even if it has no dedicated offline phrase.

T1 seed memories describe an earlier visit. Optimizing the channel changes current diagnostics for the running backend, so a repeat demonstration may show healthy Wi-Fi; restart the server/mock process for fresh fixtures. Starting a new conversation preserves state and memory, intentionally.

## Hospital

Switch to **Hospital Patient Services**. The persona changes to Maya at Crescent Hospital, with a separate set of patients, tools, knowledge, and memory namespace.

| Scenario | Prompt / actions | Expected result |
|---|---|---|
| H1 | I’d like to see a dermatologist this week. | Available doctors and UTC slots. Copy an `S-D...` ID, send “Book S-D001-YYYYMMDD-09”, and confirm. |
| H2 | Appointment with doctor D002 this week. | No slots for the doctor on leave; alternative doctors are shown. With a live model, request named doctors and alternate dates naturally. |
| H3 | Show my appointments. Then request availability. Send “Reschedule A-P003 to S-D001-YYYYMMDD-10”. | Exact appointment/slot proposal, then atomic reschedule after confirmation. “Cancel A-P003” cancels after confirmation. Use IDs actually returned for the selected patient. |
| H4 | Is DemoCare Plus insurance supported? | Supported provider and exact plan. “Is DemoCare Gold insurance supported?” is unsupported. |
| H5 | Where is visitor parking? | Visitor parking, location, opening hours and directions. |
| H6 | Mornings normally work better for me. | Preference stored asynchronously. Click **New session**, retain the same patient, then ask “Can I make another appointment with Dr. Ahmed?” Morning slots are preferred. |

Use “Show referral status” or “Show prescription status” for administrative records. No medication advice or diagnosis is provided.

For the deterministic safety demonstration, start a fresh hospital session and send “I have chest pain and can’t breathe.” The emergency response runs before any model or tools. That session remains in the urgent-assistance path. Start a new session to return to scheduling.

## Explain the platform reuse

Open **Configuration & capabilities** before and after switching. The application process is unchanged. Both agents use the same Go runtime, inference interface, memory abstraction, skill framework, typed HTTP tools, and SSE envelope. Only configuration and business adapters differ. The disabled future voice/avatar configuration marks later interface adapters without introducing those dependencies now.

KW inference and NovaMem are live. After H6 emits `memory.store.completed`, the preference survives new sessions and app deployments. A different patient must not receive it. Local offline mode remains scripted, and its default in-memory store survives only sessions within one process.
