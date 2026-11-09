package approval

import (
	"slices"

	"emperror.dev/errors"

	"github.com/webcenter-fr/eino-ext/server/approval/store"
)

// ApprovalPolicy decides whether an approver may decide a record.
//
//nolint:revive // ApprovalPolicy is the documented public name.
type ApprovalPolicy struct {
	rules []PolicyRule
}

// NewApprovalPolicy builds a policy. A nil config means self-approve.
func NewApprovalPolicy(cfg *PolicyConfig) *ApprovalPolicy {
	if cfg == nil {
		return &ApprovalPolicy{}
	}
	return &ApprovalPolicy{rules: cfg.Rules}
}

// Evaluate returns the policy name and nil when the approver may decide the
// record. It fails closed on every mismatch.
func (p *ApprovalPolicy) Evaluate(rec *store.Record, approver *Session) (string, error) {
	if rec == nil || approver == nil {
		return "", errors.New("approval policy: missing record or approver")
	}
	matched := false
	for _, rule := range p.rules {
		if !ruleMatches(rule, rec) {
			continue
		}
		matched = true
		if err := ruleAllows(rule, rec, approver); err != nil {
			return ruleName(rule), err
		}
	}
	if matched {
		return "policy", nil
	}
	// Default: self-approve. Only the requester may decide, which also blocks
	// a different user from opening another user's approval URL.
	if approver.Sub != rec.Requester.Sub {
		return "self-approve", errors.New("self-approve policy: only the requester may decide this approval")
	}
	return "self-approve", nil
}

// CanDecide reports whether the approver may decide the record.
func (p *ApprovalPolicy) CanDecide(rec *store.Record, approver *Session) bool {
	_, err := p.Evaluate(rec, approver)
	return err == nil
}

func ruleMatches(rule PolicyRule, rec *store.Record) bool {
	return matchesAny(rule.Tools, rec.ToolName) &&
		matchesAny(rule.Instances, rec.Instance) &&
		matchesAny(rule.Operations, rec.Operation)
}

func matchesAny(patterns []string, value string) bool {
	if len(patterns) == 0 {
		return true
	}
	return slices.Contains(patterns, "*") || slices.Contains(patterns, value)
}

func ruleAllows(rule PolicyRule, rec *store.Record, approver *Session) error {
	if rule.RequireFourEyes && approver.Sub == rec.Requester.Sub {
		return errors.New("four-eyes policy: the requester may not approve their own request")
	}
	if len(rule.ApproverUsers) > 0 && !slices.Contains(rule.ApproverUsers, approver.Sub) {
		return errors.New("approver is not in the allowed approver list")
	}
	if len(rule.ApproverGroups) > 0 && !intersects(rule.ApproverGroups, approver.Groups) {
		return errors.New("approver is not in a required approver group")
	}
	if !rule.RequireFourEyes && len(rule.ApproverUsers) == 0 && len(rule.ApproverGroups) == 0 {
		// A rule with no approver restriction still defaults to self-approve.
		if approver.Sub != rec.Requester.Sub {
			return errors.New("self-approve policy: only the requester may decide this approval")
		}
	}
	return nil
}

func intersects(a, b []string) bool {
	for _, v := range a {
		if slices.Contains(b, v) {
			return true
		}
	}
	return false
}

func ruleName(rule PolicyRule) string {
	if rule.Name != "" {
		return rule.Name
	}
	return "policy"
}
