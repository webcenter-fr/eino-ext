** How to limit output (IMPORTANT) **
Use `filter` (Go RE2 regex applied on alert label JSON) to narrow down to specific alerts.
RE2 does NOT support lookahead (?=...)/(?!...), lookbehind (?<=...)/(?<!...), or
backreferences — such patterns return an error. Prefer simple alternations
(e.g. 'HighCPU|HighMemory').
