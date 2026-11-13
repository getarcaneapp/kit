package updater

import (
	"context"
	"strings"
	"time"

	"github.com/moby/moby/api/types/container"
	"kit.getarcane.app"

	"kit.getarcane.app/updater/labels"
	"kit.getarcane.app/updater/refs"
)

func newTimedResult() (*Result, func(*error)) {
	out := &Result{
		Items:     make([]ResourceResult, 0),
		StartTime: time.Now().UTC(),
	}
	return out, func(err *error) {
		// keep: callers already hold out; a full 9-field Result literal to finish two fields reads worse
		out.EndTime, out.Success = time.Now().UTC(), (err == nil || *err == nil) && out.Failed == 0
	}
}

// appendResult counts item into out and appends it: every item counts as checked, and up-to-date ones as skipped too.
func appendResult(out *Result, item ResourceResult) {
	*out = Result{
		Success:   out.Success,
		Checked:   out.Checked + 1,
		Updated:   out.Updated + kit.Ternary(item.Status == StatusUpdated, 1, 0),
		Restarted: out.Restarted + kit.Ternary(item.Status == StatusRestarted, 1, 0),
		Skipped:   out.Skipped + kit.Ternary(item.Status == StatusSkipped || item.Status == StatusUpToDate, 1, 0),
		Failed:    out.Failed + kit.Ternary(item.Status == StatusFailed, 1, 0),
		StartTime: out.StartTime,
		EndTime:   out.EndTime,
		Items:     append(out.Items, item),
	}
}

// appendRecordedResult appends item to out and reports it to the RunRecorder.
func (s *Service) appendRecordedResult(ctx context.Context, out *Result, item ResourceResult) {
	appendResult(out, item)
	if s.config.RunRecorder != nil {
		_ = s.config.RunRecorder.RecordUpdateRun(ctx, item)
	}
}

func failedContainerResult(id, name, message string) ResourceResult {
	return ResourceResult{
		ResourceID:   id,
		ResourceName: name,
		ResourceType: ResourceTypeContainer,
		Status:       StatusFailed,
		Error:        message,
	}
}

func skippedContainerResult(id, name, message string) ResourceResult {
	return ResourceResult{
		ResourceID:   id,
		ResourceName: name,
		ResourceType: ResourceTypeContainer,
		Status:       StatusSkipped,
		Error:        message,
	}
}

func labelsFromInspect(inspect container.InspectResponse) map[string]string {
	if len(kit.FromPtr(inspect.Config).Labels) == 0 {
		return nil
	}
	return inspect.Config.Labels
}

// withConfiguredImage returns inspect configured with the reference recorded in LabelConfiguredImage
// when the updater ran the container from an image ID, so it keeps receiving updates.
func withConfiguredImage(inspect container.InspectResponse) container.InspectResponse {
	configured := strings.TrimSpace(kit.FromPtr(inspect.Config).Labels[labels.LabelConfiguredImage])
	if !refs.IsImageIDLikeReference(kit.FromPtr(inspect.Config).Image) || configured == "" || refs.IsImageIDLikeReference(configured) {
		return inspect
	}
	// keep: by-value copy of the shared inspected config; a full 25-field container.Config literal reads worse
	cfg := new(*inspect.Config)
	cfg.Image = configured
	// keep: inspect is the caller's by-value copy
	inspect.Config = cfg
	return inspect
}
