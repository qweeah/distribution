package storage

import (
	"context"
	"fmt"
	"path"

	"github.com/distribution/distribution/v3"
	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
)

// vacuum contains functions for cleaning up repositories and blobs
// These functions will only reliably work on strongly consistent
// storage systems.
// https://en.wikipedia.org/wiki/Consistency_model

// NewVacuum creates a new Vacuum
func NewVacuum(ctx context.Context, driver driver.StorageDriver, registry distribution.Namespace) Vacuum {
	return Vacuum{
		ctx:      ctx,
		driver:   driver,
		registry: registry,
	}
}

// Vacuum removes content from the filesystem
type Vacuum struct {
	driver   driver.StorageDriver
	ctx      context.Context
	registry distribution.Namespace
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
		if _, ok := err.(driver.PathNotFoundError); !ok {
			return err
		}
	}

	for _, extNamespace := range v.registry.Extensions() {
		handlers := extNamespace.GetGarbageCollectionHandlers()
		for _, gcHandler := range handlers {
			err := gcHandler.RemoveManifestVacuum(v.ctx, v.driver, dgst, name)
			if err != nil {
				return fmt.Errorf("failed to call remove manifest extension handler: %v", err)
			}
		}
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
