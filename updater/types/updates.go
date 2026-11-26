// Package types contains tag update policies and adapter contracts.
package types

import "context"

type (
	// Policy controls image updates. The zero policy follows the current tag's digest; a constraint or
	// tag pattern, or strategy auto, selects tag updates for stable complete semantic versions.
	Policy struct {
		Strategy   string `json:"strategy,omitempty"`
		Constraint string `json:"constraint,omitempty"`
		TagPattern string `json:"tagPattern,omitempty"`
	}

	// RegistryTagLister lists every tag in an image's repository or returns an error.
	RegistryTagLister interface {
		ListTags(ctx context.Context, imageRef string) ([]string, error)
	}

	// TagCandidate describes a newer tag that tag selection picked. ImageID is the image the container
	// runs, or empty when the check has no container.
	TagCandidate struct {
		CurrentRef string `json:"currentRef"`
		TargetRef  string `json:"targetRef"`
		ImageID    string `json:"imageId,omitempty"`
	}

	// TagCandidateFilter reports whether a selected tag can be used, for example whether it has a
	// manifest for the running platform. A rejected tag is dropped and selection runs again; an error
	// keeps the candidate unless the check's context is done, which fails the check.
	TagCandidateFilter interface {
		AcceptTag(ctx context.Context, candidate TagCandidate) (bool, error)
	}

	// CheckRequest describes an image and the digest currently in use. CurrentDigest
	// can be omitted when the local Docker engine has the image.
	CheckRequest struct {
		ImageRef      string `json:"imageRef"`
		CurrentDigest string `json:"currentDigest,omitempty"`
		Policy        Policy `json:"policy"`
	}

	// CheckResult describes a read-only update check. Ineligible references have a
	// reason and no available update. Errors are returned separately.
	CheckResult struct {
		ContainerID     string `json:"containerId,omitempty"`
		CurrentRef      string `json:"currentRef"`
		TargetRef       string `json:"targetRef"`
		CurrentVersion  string `json:"currentVersion,omitempty"`
		TargetVersion   string `json:"targetVersion,omitempty"`
		CurrentDigest   string `json:"currentDigest,omitempty"`
		TargetDigest    string `json:"targetDigest,omitempty"`
		UpdateType      string `json:"updateType,omitempty"`
		Reason          string `json:"reason,omitempty"`
		UpdateAvailable bool   `json:"updateAvailable"`
	}

	// ServiceImageChange describes a persisted Compose image replacement. Adapters
	// must reject a service whose configured reference no longer matches ExpectedRef.
	ServiceImageChange struct {
		ExpectedRef string `json:"expectedRef"`
		TargetRef   string `json:"targetRef"`
	}

	// ProjectImageUpdater persists service image changes and recreates those services; success guarantees both.
	// Implement it on the configured ProjectUpdater to support tag changes.
	ProjectImageUpdater interface {
		UpdateServiceImages(ctx context.Context, projectID string, changes map[string]ServiceImageChange) error
	}
)
