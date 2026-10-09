// Package labels reads the container labels that tell the updater whether a container opts out, is an
// Arcane server or agent that updates itself, belongs to Swarm, and how it wants to be stopped.
package labels

import (
	"strings"

	kit "go.getarcane.app/kit/pkg"
)

const (
	// LabelArcane identifies an Arcane server container.
	LabelArcane = "com.getarcaneapp.arcane"
	// LabelArcaneLegacyServer identifies pre-migration Arcane server containers.
	LabelArcaneLegacyServer = "com.getarcaneapp.arcane.server"
	// LabelArcaneAgent identifies an Arcane agent container.
	LabelArcaneAgent = "com.getarcaneapp.arcane.agent"
	// LabelUpdater controls updater participation.
	LabelUpdater = "com.getarcaneapp.arcane.updater"
	// LabelUpdateStrategy selects digest or tag updates.
	LabelUpdateStrategy = LabelUpdater + ".strategy"
	// LabelUpdateConstraint limits eligible semantic versions.
	LabelUpdateConstraint = LabelUpdater + ".constraint"
	// LabelUpdateTagPattern selects tags and optionally captures their version.
	LabelUpdateTagPattern = LabelUpdater + ".tag-pattern"
	// LabelSwarmServiceID identifies a Docker Swarm task.
	LabelSwarmServiceID = "com.docker.swarm.service.id"
	// LabelSwarmServiceName identifies a Docker Swarm task.
	LabelSwarmServiceName = "com.docker.swarm.service.name"
	// LabelDependsOn declares updater restart dependencies.
	LabelDependsOn = "com.getarcaneapp.arcane.depends-on"
	// LabelStopSignal declares a custom stop signal.
	LabelStopSignal = "com.getarcaneapp.arcane.stop-signal"
)

// IsArcaneContainer reports whether labels identify an Arcane self-update target.
func IsArcaneContainer(labels map[string]string) bool {
	return hasTruthyLabel(labels, LabelArcane) || hasTruthyLabel(labels, LabelArcaneLegacyServer) || IsArcaneAgentContainer(labels)
}

// IsArcaneServerContainer reports whether labels identify an Arcane server.
func IsArcaneServerContainer(labels map[string]string) bool {
	return (hasTruthyLabel(labels, LabelArcane) || hasTruthyLabel(labels, LabelArcaneLegacyServer)) && !IsArcaneAgentContainer(labels)
}

// ShouldDisableArcaneServerRedeploy reports whether redeploy or edit should be
// blocked because the container is the running Arcane server or agent.
func ShouldDisableArcaneServerRedeploy(labels map[string]string, containerID, currentContainerID string, currentErr error) bool {
	if !IsArcaneContainer(labels) {
		return false
	}

	current := strings.TrimSpace(currentContainerID)
	if currentErr != nil || current == "" {
		return true
	}
	containerID = strings.TrimSpace(containerID)
	return containerID != "" && (strings.HasPrefix(containerID, current) || strings.HasPrefix(current, containerID))
}

// IsArcaneAgentContainer reports whether labels identify an Arcane agent.
//
//nolint:shimbad // exported predicate binding the agent label constant; part of the public API.
func IsArcaneAgentContainer(labels map[string]string) bool {
	return hasTruthyLabel(labels, LabelArcaneAgent)
}

// IsUpdateDisabled reports whether labels opt out of updates.
func IsUpdateDisabled(labels map[string]string) bool {
	value, ok := lookupLabel(labels, LabelUpdater)
	enabled, recognized := kit.ParseBool(value)
	return ok && recognized && !enabled
}

// IsSwarmTask reports whether labels identify a Docker Swarm task.
func IsSwarmTask(labels map[string]string) bool {
	serviceID, _ := lookupLabel(labels, LabelSwarmServiceID)
	serviceName, _ := lookupLabel(labels, LabelSwarmServiceName)
	return strings.TrimSpace(serviceID) != "" || strings.TrimSpace(serviceName) != ""
}

// StopSignal returns a custom stop signal from labels.
func StopSignal(labels map[string]string) string {
	value, ok := lookupLabel(labels, LabelStopSignal)
	if !ok {
		return ""
	}
	return strings.TrimSpace(strings.ToUpper(value))
}

func hasTruthyLabel(labels map[string]string, target string) bool {
	value, ok := lookupLabel(labels, target)
	truthy, _ := kit.ParseBool(value)
	return ok && truthy
}

func lookupLabel(labels map[string]string, target string) (string, bool) {
	for key, value := range labels {
		if strings.EqualFold(key, target) {
			return value, true
		}
	}
	return "", false
}
