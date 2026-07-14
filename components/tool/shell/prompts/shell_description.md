
** General Purpose **
It executes a shell command in an isolated Dagger container sandbox backed by an OCI base image (e.g., golang, node, python).

The container runs as root, so you can install additional tools with apt-get, pip, npm, go install, etc. The command output can be filtered using a regex pattern.

** IMPORTANT RULES **
- Use this tool to run commands in a sandboxed environment — never touch the host filesystem.
- Commands matching a known destructive pattern (e.g., 'rm', 'kill', 'shutdown') are automatically blocked.
- This is a WRITE tool: you must call it with dryRun=true first to preview the command, then re-call with confirmed=true after user approval.
- Each session maintains a persistent container across multiple commands — you can install tools once and reuse them in subsequent commands.

** Security **
- Network egress is restricted by default. Local network access (RFC1918, link-local, cloud metadata) is denied unless AllowLocalNetwork is set to true.
- Package mirrors specified in the egress allowlist are permitted.

** Output **
It returns a string representing the command output (stdout followed by stderr). Each output line is separated by a newline character.
