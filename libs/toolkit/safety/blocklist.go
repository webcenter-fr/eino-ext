package safety

import (
	"regexp"
	"strings"

	"emperror.dev/errors"
)

// DefaultCommandBlocklist contains regex patterns for commands blocked by default.
var DefaultCommandBlocklist = []string{
	`\brm\b`,
	`\brmdir\b`,
	`\bkill\b`,
	`\bkillall\b`,
	`\bpkill\b`,
	`\bshutdown\b`,
	`\breboot\b`,
	`\bhalt\b`,
	`\bpoweroff\b`,
	`\bdd\b`,
	`\bmkfs\w*`,
	`\bmkswap\b`,
	`\bmount\b`,
	`\bumount\b`,
	`\bswapon\b`,
	`\bswapoff\b`,
	`\bchmod\s+.*(--recursive|-R)\s+/`,
	`\bchown\s+.*(--recursive|-R)\s+/`,
	`\bchroot\b`,
	`\binsmod\b`,
	`\bmodprobe\b`,
	`\biptables\b`,
	`\bsystemctl\s+stop\b`,
	`\bsystemctl\s+disable\b`,
	`\bsystemctl\s+mask\b`,
	`>.*/dev/`,
	`\b(?:/usr/bin/|/bin/)?(?:ba|da|z)?sh\b`,
	`\b(?:/usr/bin/|/bin/)?(?:ba|da|z)?sh\s+-c\b`,
	`\b(?:/usr/bin/|/bin/)?python(?:3)?\s+-c\b`,
	`\b(?:/usr/bin/|/bin/)?python(?:3)?\s+-m\b`,
	`\b(?:/usr/bin/|/bin/)?perl\s+-e\b`,
	`\b(?:/usr/bin/|/bin/)?ruby\s+-e\b`,
	`\b(?:/usr/bin/|/bin/)?node\s+-e\b`,
	`\b(?:/usr/bin/|/bin/)?php\s+-r\b`,
	`\b(?:/usr/bin/|/bin/)?env\s+`,
	`\b(?:/usr/bin/|/bin/)?busybox\b`,
	`\b(?:/usr/bin/|/bin/)?toybox\b`,
	`\beval\b`,
	`\bsource\b`,
	`^\s*\.\s+`,
	`\bg?awk\b`,
	`\bnawk\b`,
	`\btar\s+.*--to-command`,
	`\bxargs\b`,
	`\binstall\b`,
	`\bcpio\b`,
	`\bscreen\b`,
	`\btmux\b`,
	`\bscript\b`,
	`\bexpect\b`,
	`\btee\b`,
	`\b(?:/usr/bin/|/bin/)?execlineb\b`,
	`\bopenssl\s+enc\b`,
}

// CompileBlocklist compiles a list of regex patterns for command blocking.
func CompileBlocklist(patterns []string) ([]*regexp.Regexp, error) {
	result := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, errors.Wrapf(err, "invalid blocklist pattern %q", p)
		}
		result = append(result, re)
	}
	return result, nil
}

// CheckBlocklist checks if a command matches any pattern in the compiled blocklist.
func CheckBlocklist(compiled []*regexp.Regexp, command []string) error {
	cmdStr := strings.Join(command, " ")
	for _, re := range compiled {
		if re.MatchString(cmdStr) {
			return errors.Errorf("command %q is blocked by security policy (matches blocklist pattern %q)", cmdStr, re.String())
		}
	}
	return nil
}
