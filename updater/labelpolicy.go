package updater

import (
	"strings"

	"kit.getarcane.app"

	"kit.getarcane.app/updater/labels"
	"kit.getarcane.app/updater/types"
)

// LabelPolicy decides from a container's labels whether the updater may touch it, whether the host must
// update it itself, and how to stop it. New fills nil funcs from DefaultLabelPolicy.
type LabelPolicy struct {
	TagPolicyFunc          func(map[string]string) types.Policy
	IsUpdateDisabledFunc   func(map[string]string) bool
	IsSelfUpdateTargetFunc func(map[string]string) bool
	IsAgentFunc            func(map[string]string) bool
	IsServerFunc           func(map[string]string) bool
	IsSwarmTaskFunc        func(map[string]string) bool
	StopSignalFunc         func(map[string]string) string
	UnmanagedReasonFunc    func(map[string]string) string
}

// DefaultLabelPolicy returns the Arcane-compatible label behavior New uses for
// any field a caller leaves nil. It sets no UnmanagedReasonFunc, so by default
// no container is reported as managed by another tool.
func DefaultLabelPolicy() LabelPolicy {
	return LabelPolicy{
		TagPolicyFunc: func(l map[string]string) types.Policy {
			return types.Policy{
				Strategy:   strings.TrimSpace(l[labels.LabelUpdateStrategy]),
				Constraint: strings.TrimSpace(l[labels.LabelUpdateConstraint]),
				TagPattern: strings.TrimSpace(l[labels.LabelUpdateTagPattern]),
			}
		},
		IsUpdateDisabledFunc:   labels.IsUpdateDisabled,
		IsSelfUpdateTargetFunc: labels.IsArcaneContainer,
		IsAgentFunc:            labels.IsArcaneAgentContainer,
		IsServerFunc:           labels.IsArcaneServerContainer,
		IsSwarmTaskFunc:        labels.IsSwarmTask,
		StopSignalFunc:         labels.StopSignal,
	}
}

// Merge returns p with every non-nil func from o laid over it. Neither p nor o is changed,
// so DefaultLabelPolicy().Merge(custom) overrides only the behaviors custom sets.
func (p LabelPolicy) Merge(o LabelPolicy) LabelPolicy {
	return LabelPolicy{
		TagPolicyFunc:          kit.Ternary(o.TagPolicyFunc == nil, p.TagPolicyFunc, o.TagPolicyFunc),
		IsUpdateDisabledFunc:   kit.Ternary(o.IsUpdateDisabledFunc == nil, p.IsUpdateDisabledFunc, o.IsUpdateDisabledFunc),
		IsSelfUpdateTargetFunc: kit.Ternary(o.IsSelfUpdateTargetFunc == nil, p.IsSelfUpdateTargetFunc, o.IsSelfUpdateTargetFunc),
		IsAgentFunc:            kit.Ternary(o.IsAgentFunc == nil, p.IsAgentFunc, o.IsAgentFunc),
		IsServerFunc:           kit.Ternary(o.IsServerFunc == nil, p.IsServerFunc, o.IsServerFunc),
		IsSwarmTaskFunc:        kit.Ternary(o.IsSwarmTaskFunc == nil, p.IsSwarmTaskFunc, o.IsSwarmTaskFunc),
		StopSignalFunc:         kit.Ternary(o.StopSignalFunc == nil, p.StopSignalFunc, o.StopSignalFunc),
		UnmanagedReasonFunc:    kit.Ternary(o.UnmanagedReasonFunc == nil, p.UnmanagedReasonFunc, o.UnmanagedReasonFunc),
	}
}

// IsUpdateDisabled reports whether labels opt the container out of updates.
func (p LabelPolicy) IsUpdateDisabled(containerLabels map[string]string) bool {
	return p.IsUpdateDisabledFunc != nil && p.IsUpdateDisabledFunc(containerLabels)
}

// IsSelfUpdateTarget reports whether labels require host self-update handling.
func (p LabelPolicy) IsSelfUpdateTarget(containerLabels map[string]string) bool {
	return p.IsSelfUpdateTargetFunc != nil && p.IsSelfUpdateTargetFunc(containerLabels)
}

// IsAgent reports whether labels identify an agent self-update target.
func (p LabelPolicy) IsAgent(containerLabels map[string]string) bool {
	return p.IsAgentFunc != nil && p.IsAgentFunc(containerLabels)
}

// IsServer reports whether labels identify a server self-update target.
func (p LabelPolicy) IsServer(containerLabels map[string]string) bool {
	return p.IsServerFunc != nil && p.IsServerFunc(containerLabels)
}

// IsSwarmTask reports whether labels identify a Docker Swarm task container.
func (p LabelPolicy) IsSwarmTask(containerLabels map[string]string) bool {
	return p.IsSwarmTaskFunc != nil && p.IsSwarmTaskFunc(containerLabels)
}

// UnmanagedReason reports why another tool owns the container's updates, or "" when the updater may.
func (p LabelPolicy) UnmanagedReason(containerLabels map[string]string) string {
	if p.UnmanagedReasonFunc == nil {
		return ""
	}
	return p.UnmanagedReasonFunc(containerLabels)
}

// StopSignal returns the configured stop signal for a container, or "" when the
// policy declares none.
func (p LabelPolicy) StopSignal(containerLabels map[string]string) string {
	if p.StopSignalFunc == nil {
		return ""
	}
	return p.StopSignalFunc(containerLabels)
}

// TagPolicy returns the image selection policy for a container.
func (p LabelPolicy) TagPolicy(containerLabels map[string]string) types.Policy {
	if p.TagPolicyFunc == nil {
		return types.Policy{}
	}
	return p.TagPolicyFunc(containerLabels)
}
