package updater

import (
	"context"
	"time"

	"github.com/moby/moby/api/types/container"
)

func newTimedResult() (*Result, func(*error)) {
	out := &Result{
		Items:     make([]ResourceResult, 0),
		StartTime: time.Now().UTC(),
	}
	return out, func(err *error) {
		out.EndTime = time.Now().UTC()
		out.Success = (err == nil || *err == nil) && out.Failed == 0
	}
}

// appendResult counts item into out and appends it: every item counts as checked, and up-to-date ones as skipped too.
func appendResult(out *Result, item ResourceResult) {
	out.Checked++
	switch item.Status {
	case StatusUpdated:
		out.Updated++
	case StatusRestarted:
		out.Restarted++
	case StatusSkipped, StatusUpToDate:
		out.Skipped++
	case StatusFailed:
		out.Failed++
	case StatusChecked, StatusUpdateAvailable:
	}
	out.Items = append(out.Items, item)
}

// appendRecordedResult appends item to out and reports it to the RunRecorder.
func (s *Service) appendRecordedResult(ctx context.Context, out *Result, item ResourceResult) {
	appendResult(out, item)
	if s.config.RunRecorder != nil {
		_ = s.config.RunRecorder.RecordUpdateRun(ctx, item)
	}
}

func failedContainerResult(id, name, message string) ResourceResult {
	return ResourceResult{ResourceID: id, ResourceName: name, ResourceType: ResourceTypeContainer, Status: StatusFailed, Error: message}
}

func skippedContainerResult(id, name, message string) ResourceResult {
	return ResourceResult{ResourceID: id, ResourceName: name, ResourceType: ResourceTypeContainer, Status: StatusSkipped, Error: message}
}

func labelsFromInspect(inspect container.InspectResponse) map[string]string {
	if inspect.Config == nil || len(inspect.Config.Labels) == 0 {
		return nil
	}
	return inspect.Config.Labels
}
