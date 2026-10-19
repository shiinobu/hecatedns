---
description: Systematic debugging for hecate - reproduce, isolate, find the root cause with evidence, write a failing test, fix, verify. Use for failing tests, crashes, wrong DNS answers, or findings from the phone test.
---

# /debug <symptom>

No fix before a root cause. Guessing and patching symptoms is the failure mode.

1. **State the symptom precisely:** exact command, input, expected vs actual output, error text. For a phone-test problem add: the time of the event, the client IP, the domain involved.
2. **Reproduce** with the smallest case: a failing unit test, or a `scripts/dnsquery.ps1 <name> -Port 5300` against a locally running `hecate run -config <scratch config>`. If it does not reproduce, say so and collect more evidence instead of changing code.
3. **Gather evidence, not theories:**
   - The query log: `hecate log -around HH:MM -window 30s` (or `-grep <domain>`, `-blocked`). It shows `action`, `reason`, `source`, `upstream`, `rcode`, `ms`, `via`.
   - Compare the engine decision with the backend result: was it `Before` (rule/list/acl/special), `After` (cname), or the upstream?
   - Check the data: is the domain in `data/rules.txt` or a cached list (`data/lists/<name>.txt`)? Does the matcher normalize it (case, trailing dot)?
   - Read the relevant code path end to end once; do not skim.
4. **Form one hypothesis at a time**, with the prediction that would prove it wrong, and test it (a log line, a unit test, a scratch program).
5. **Root cause found?** Write the failing test that captures it (RED). If you cannot write one, you probably do not understand it yet.
6. **Fix minimally (GREEN),** then run the package tests, the race run if concurrency is involved, and `/gates` if the fix touches shared behavior.
7. **Look for siblings:** the same mistake in similar code (other parsers, other modes, the native backend). Add tests for them or note them.
8. **Record** in the WORKLOG: symptom, root cause, evidence, fix commit, what to watch. Add dead ends to `notes/do-not-retry.md`.

Typical hecate suspects: suffix-matching mistakes (`notexample.com` vs `example.com`), uppercase or trailing-dot names, IPv4-mapped IPv6 clients, an HTTPS/SVCB (type 65) answer bypassing a block, a cached answer skipping the CNAME check, a rules reload that missed an mtime change, an ICS/port-53 binding conflict, the phone using its own DoH/Private DNS instead of hecate, IPv6 DNS handed out by the router.

If three fix cycles fail, stop and write the evidence and your best diagnosis into the WORKLOG for the next session.
