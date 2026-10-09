# Sales demo walkthrough

The KW deployment uses Qwen3.5-9B and live NovaMem. For a local scripted walkthrough, use the default offline provider. Each scripted interaction invokes the same HTTP tools that a live model uses. Tool outputs and actual state changes are visible in the activity panel. A live model enables broader phrasing; offline action requests use the IDs shown in the conversation.

## Telecom

Select **Telecom Support**, the indicated customer, and **Start session**.

| Scenario | Identity           | Prompt                                  | Expected result                                                                                                                                   |
| -------- | ------------------ | --------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| T1       | C001 Pascal Martin | My internet upstairs is terrible again. | Retrieves prior interference and troubleshooting preference; checks outage, line, and Wi-Fi. Follow with “Optimize my Wi-Fi channel” and confirm. |
| T2       | C002 Leila Haddad  | My internet is down.                    | Regional outage; equipment troubleshooting is skipped. “Restart my router” does not offer a restart.                                              |
| T3       | C003 Noah Brooks   | My internet is unavailable.             | Restricted service and outstanding balance. “Explain my bill” shows the overdue invoice.                                                          |
| T4       | C004 Aisha Khan    | Explain my bill.                        | $105 total versus $50 previously: $35 roaming and $20 installation.                                                                               |
| T5       | C005 Ethan Cole    | My data allowance is exhausted.         | 20/20 GB used, throttling, usage history, actual plan catalog.                                                                                    |
| T6       | C006 Sofia Reyes   | Restart my router.                      | Checks access status, offers confirmation, then repairs router health.                                                                            |
| T7       | C007 Omar Farouk   | My internet is broken.                  | Physical line fault and available technician slots. “Book technician TECH-01” and confirm.                                                        |
| T8       | C008 Maya Chen     | Compare available plans.                | Current subscription, usage, and actual prices. “Change plan to fiber-500” and confirm.                                                           |

Other offline examples: “Show my account”, “Create ticket for recurring upstairs coverage”, “Show tickets”, “Close ticket T-C001”. Every configured tool is available to a live model, even if it has no dedicated offline phrase.

T1 seed memories describe an earlier visit. Optimizing the channel changes current diagnostics for the running backend, so a repeat demonstration may show healthy Wi-Fi; restart the server/mock process for fresh fixtures. Starting a new conversation preserves state and memory, intentionally.

## Explain the platform reuse

Open **Configuration & capabilities** before and after switching. The application process is unchanged. Both agents use the same Go runtime, inference interface, memory abstraction, skill framework, typed HTTP tools, and SSE envelope. Only configuration and business adapters differ. The disabled future voice/avatar configuration marks later interface adapters without introducing those dependencies now.

KW inference and NovaMem are live. After a stated preference emits `memory.store.completed`, it survives new sessions and app deployments. A different customer must not receive it. Local offline mode remains scripted, and its default in-memory store survives only sessions within one process.
