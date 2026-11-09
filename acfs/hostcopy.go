package acfs

import (
	"context"

	acfstypes "kit.getarcane.app/acfs/types"
)

// CopyDir copies the host directory sourceDir into destinationDir like Root.CopyTo.
func (Host) CopyDir(ctx context.Context, sourceDir, destinationDir string, options acfstypes.CopyOptions) (acfstypes.CopyResult, error) {
	source, err := cleanHostPath(sourceDir)
	if err != nil {
		return acfstypes.CopyResult{}, err
	}
	destination, err := cleanHostPath(destinationDir)
	if err != nil {
		return acfstypes.CopyResult{}, err
	}
	return Root{Path: source}.CopyTo(ctx, Root{Path: destination}, options)
}

// MirrorDir makes the host directory destinationDir match sourceDir like Root.MirrorTo.
func (Host) MirrorDir(ctx context.Context, sourceDir, destinationDir string, options acfstypes.MirrorOptions) error {
	source, err := cleanHostPath(sourceDir)
	if err != nil {
		return err
	}
	destination, err := cleanHostPath(destinationDir)
	if err != nil {
		return err
	}
	return Root{Path: source}.MirrorTo(ctx, Root{Path: destination}, options)
}
