package match

import (
	"cmp"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"

	"go.getarcane.app/updater/digest"
	"go.getarcane.app/updater/internal/compose"
	"go.getarcane.app/updater/refs"
)

// AppendImageUpdateRecordIDToOldIDs includes SHA-like update record IDs in the old-image match set.
func AppendImageUpdateRecordIDToOldIDs(oldIDs []string, recordID string) []string {
	recordID = strings.TrimSpace(recordID)
	if !refs.IsImageIDLikeReference(recordID) || slices.Contains(oldIDs, recordID) {
		return oldIDs
	}
	return append(oldIDs, recordID)
}

// ResolveContainerImageMatch finds the new image reference for a running container.
func ResolveContainerImageMatch(c container.Summary, inspect *container.InspectResponse, oldIDToNewRef, updatedNorm map[string]string) (newRef, match string) {
	if nr, ok := oldIDToNewRef[c.ImageID]; ok && c.ImageID != "" {
		return nr, c.ImageID
	}
	if inspect != nil && inspect.Image != "" {
		if nr, ok := oldIDToNewRef[inspect.Image]; ok {
			return nr, inspect.Image
		}
	}
	candidates := []string{c.Image}
	if inspect != nil {
		if inspect.Config != nil {
			candidates = append(candidates, inspect.Config.Image)
		}
		candidates = append(candidates, inspect.Image)
	}
	for _, imageRef := range candidates {
		if refs.IsImageIDLikeReference(imageRef) {
			continue
		}
		norm := refs.NormalizeImageUpdateRef(imageRef)
		if nr, ok := updatedNorm[norm]; ok && norm != "" {
			return nr, norm
		}
	}
	return "", ""
}

// ShouldInspectUnmatchedContainerForImageMatch reports whether inspect may recover a tag match.
func ShouldInspectUnmatchedContainerForImageMatch(c container.Summary) bool {
	if strings.TrimSpace(c.Image) == "" || refs.IsImageIDLikeReference(c.Image) {
		return true
	}
	if _, isDigestRef := digest.FromReferenceSuffix(c.Image); !isDigestRef {
		return false
	}
	return compose.ProjectLabel(c.Labels) != "" || compose.ServiceLabel(c.Labels) != ""
}

// CurrentContainerImageID returns the best available image ID for a container.
func CurrentContainerImageID(c container.Summary, inspect *container.InspectResponse) string {
	if inspect == nil {
		return strings.TrimSpace(c.ImageID)
	}
	return cmp.Or(strings.TrimSpace(c.ImageID), strings.TrimSpace(inspect.Image))
}
