package updater

import (
	"context"
	"maps"
	"strings"

	kit "go.getarcane.app/kit/pkg"
)

func (s *Service) triggerSelfUpdate(ctx context.Context, containerID, containerName, newImageRef string, labels map[string]string) error {
	if s.config.SelfUpdater == nil {
		return ErrSelfUpdaterRequired
	}
	instanceType := kit.Ternary(s.config.LabelPolicy.IsAgent(labels), "agent", "server")
	_ = s.recordEvent(ctx, "self_update_trigger", containerID, containerName, map[string]any{
		"instanceType": instanceType,
		"newImage":     newImageRef,
	})
	return s.config.SelfUpdater.TriggerSelfUpdate(ctx, SelfUpdateTarget{
		ContainerID:   containerID,
		ContainerName: containerName,
		InstanceType:  instanceType,
		Labels:        maps.Clone(labels),
		NewImageRef:   newImageRef,
	})
}

// isSelfUpdateCandidate reports whether the host SelfUpdater must handle a container, by label policy
// or because the host application runs in it.
func (s *Service) isSelfUpdateCandidate(containerID string, labels map[string]string) bool {
	if s.config.LabelPolicy.IsSelfUpdateTarget(labels) {
		return true
	}
	selfID := strings.TrimSpace(s.config.SelfContainerID)
	return selfID != "" && containerID != "" && (strings.HasPrefix(containerID, selfID) || strings.HasPrefix(selfID, containerID))
}

func (s *Service) notify(ctx context.Context, containerID, containerName, imageRef, oldImage, newImage string) error {
	if s.config.Notifier == nil {
		return nil
	}
	return s.config.Notifier.Notify(ctx, Notification{
		ContainerID:   containerID,
		ContainerName: containerName,
		ImageRef:      imageRef,
		OldImage:      oldImage,
		NewImage:      newImage,
	})
}

// recordEvent records a container-scoped update event; every event the updater emits concerns a container.
func (s *Service) recordEvent(ctx context.Context, phase, resourceID, resourceName string, metadata map[string]any) error {
	if s.config.EventRecorder == nil {
		return nil
	}
	return s.config.EventRecorder.RecordEvent(ctx, Event{
		Phase:        phase,
		Severity:     "info",
		ResourceID:   resourceID,
		ResourceName: resourceName,
		ResourceType: ResourceTypeContainer,
		Metadata:     metadata,
	})
}
