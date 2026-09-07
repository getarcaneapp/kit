// Package types contains tag update policies and adapter contracts.
package types

import "context"

// Policy controls image updates. The zero policy infers tag updates for stable
// complete semantic versions and digest updates for moving or ambiguous tags.
type Policy struct {
	Strategy   string `json:"strategy,omitempty"`
	Constraint string `json:"constraint,omitempty"`
	TagPattern string `json:"tagPattern,omitempty"`
}

// RegistryTagLister lists every tag in an image's repository or returns an error.
type RegistryTagLister interface {
	ListTags(ctx context.Context, imageRef string) ([]string, error)
}

// CheckRequest describes an image and the digest currently in use. CurrentDigest
// can be omitted when the local Docker engine has the image.
type CheckRequest struct {
	ImageRef      string `json:"imageRef"`
	CurrentDigest string `json:"currentDigest,omitempty"`
	Policy        Policy `json:"policy"`
}

// CheckResult describes a read-only update check. Ineligible references have a
// reason and no available update. Errors are returned separately.
type CheckResult struct {
	ContainerID     string `json:"containerId,omitempty"`
	CurrentRef      string `json:"currentRef"`
	TargetRef       string `json:"targetRef"`
	CurrentVersion  string `json:"currentVersion,omitempty"`
	TargetVersion   string `json:"targetVersion,omitempty"`
	CurrentDigest   string `json:"currentDigest,omitempty"`
	TargetDigest    string `json:"targetDigest,omitempty"`
	UpdateType      string `json:"updateType,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Reason          string `json:"reason,omitempty"`
}

// ServiceImageChange describes a persisted Compose image replacement. Adapters
// must reject a service whose configured reference no longer matches ExpectedRef.
type ServiceImageChange struct {
	ExpectedRef string `json:"expectedRef"`
	TargetRef   string `json:"targetRef"`
}

// ProjectImageUpdater persists service image changes and recreates those services.
// Returning success guarantees persistence as well as recreation. Implement this
// optional interface on the configured ProjectUpdater to support tag changes.
type ProjectImageUpdater interface {
	UpdateServiceImages(ctx context.Context, projectID string, changes map[string]ServiceImageChange) error
}
