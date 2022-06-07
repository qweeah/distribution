package storage

import (
	"context"
	"path"

	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
)

// vacuum contains functions for cleaning up repositories and blobs
// These functions will only reliably work on strongly consistent
// storage systems.
// https://en.wikipedia.org/wiki/Consistency_model

// NewVacuum creates a new Vacuum
func NewVacuum(ctx context.Context, driver driver.StorageDriver) Vacuum {
	return Vacuum{
		ctx:    ctx,
		driver: driver,
	}
}

// Vacuum removes content from the filesystem
type Vacuum struct {
	driver driver.StorageDriver
	ctx    context.Context
}

// RemoveBlob removes a blob from the filesystem
func (v Vacuum) RemoveBlob(dgst string) error {
	d, err := digest.Parse(dgst)
	if err != nil {
		return err
	}

	blobPath, err := pathFor(blobPathSpec{digest: d})
	if err != nil {
		return err
	}

	dcontext.GetLogger(v.ctx).Infof("Deleting blob: %s", blobPath)

	err = v.driver.Delete(v.ctx, blobPath)
	if err != nil {
		return err
	}

	return nil
}

// RemoveManifest removes a manifest from the filesystem
// Removes manifest's ref folder if it exists
func (v Vacuum) RemoveManifest(name string, dgst digest.Digest, tags []string) error {
	// remove a tag manifest reference, in case of not found continue to next one
	for _, tag := range tags {

		tagsPath, err := pathFor(manifestTagIndexEntryPathSpec{name: name, revision: dgst, tag: tag})
		if err != nil {
			return err
		}

		_, err = v.driver.Stat(v.ctx, tagsPath)
		if err != nil {
			switch err := err.(type) {
			case driver.PathNotFoundError:
				continue
			default:
				return err
			}
		}
		dcontext.GetLogger(v.ctx).Infof("deleting manifest tag reference: %s", tagsPath)
		err = v.driver.Delete(v.ctx, tagsPath)
		if err != nil {
			return err
		}
	}

	manifestPath, err := pathFor(manifestRevisionPathSpec{name: name, revision: dgst})
	if err != nil {
		return err
	}
	dcontext.GetLogger(v.ctx).Infof("deleting manifest: %s", manifestPath)
	err = v.driver.Delete(v.ctx, manifestPath)
	if err != nil {
		return err
	}

	referrerRootPath, err := pathFor(referrersRootPathSpec{name: name})
	if err != nil {
		return err
	}
	fullArtifactManifestPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
	dcontext.GetLogger(v.ctx).Infof("deleting manifest ref folder: %s", fullArtifactManifestPath)
	v.driver.Delete(v.ctx, fullArtifactManifestPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return nil
}

// RemoveRepository removes a repository directory from the
// filesystem
func (v Vacuum) RemoveRepository(repoName string) error {
	rootForRepository, err := pathFor(repositoriesRootPathSpec{})
	if err != nil {
		return err
	}
	repoDir := path.Join(rootForRepository, repoName)
	dcontext.GetLogger(v.ctx).Infof("Deleting repo: %s", repoDir)
	err = v.driver.Delete(v.ctx, repoDir)
	if err != nil {
		return err
	}

	return nil
}

// RemoveArtifactManifest removes a artifact manifest from the filesystem
// Removes manifest revision file and manifest ref folder if it exists
func (v Vacuum) RemoveArtifactManifest(name string, artifactDgst digest.Digest) error {
	manifestPath, err := pathFor(manifestRevisionPathSpec{name: name, revision: artifactDgst})
	if err != nil {
		return err
	}
	dcontext.GetLogger(v.ctx).Infof("deleting artifact manifest: %s", manifestPath)
	err = v.driver.Delete(v.ctx, manifestPath)
	if err != nil {
		return err
	}

	referrerRootPath, err := pathFor(referrersRootPathSpec{name: name})
	if err != nil {
		return err
	}
	fullArtifactManifestPath := path.Join(referrerRootPath, artifactDgst.Algorithm().String(), artifactDgst.Hex())
	dcontext.GetLogger(v.ctx).Infof("deleting artifact manifest ref: %s", fullArtifactManifestPath)
	err = v.driver.Delete(v.ctx, fullArtifactManifestPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return nil
}
