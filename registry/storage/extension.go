package storage

import (
	"context"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	storagedriver "github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
)

// ReadOnlyBlobStore represent the suite of readonly operations for blobs.
type ReadOnlyBlobStore interface {
	distribution.BlobEnumerator
	distribution.BlobStatter
	distribution.BlobProvider
}

type GCExtensionHandler interface {
	Mark(ctx context.Context,
		repository distribution.Repository,
		storageDriver driver.StorageDriver,
		registry distribution.Namespace,
		manifest distribution.Manifest,
		manifestDigest digest.Digest,
		dryRun bool,
		removeUntagged bool) (bool, error)
	RemoveManifest(ctx context.Context,
		storageDriver driver.StorageDriver,
		registry distribution.Namespace,
		dgst digest.Digest,
		repositoryName string) error
	SweepBlobs(ctx context.Context,
		markSet map[digest.Digest]struct{}) map[digest.Digest]struct{}
}

// ExtendedStorage defines extensions to store operations like manifest for example.
type ExtendedStorage interface {
	// GetManifestHandlers returns the list of manifest handlers that handle custom manifest formats supported by the extensions.
	GetManifestHandlers(
		repo distribution.Repository,
		blobStore distribution.BlobStore) []ManifestHandler
	// GetGarbageCollectHandlers returns the list of GC handlers that handle custom garbage collection behavior for the extensions
	GetGarbageCollectionHandlers() []GCExtensionHandler
}

// GetManifestLinkReadOnlyBlobStore will enable extensions to access the underlying linked blob store for readonly operations.
// This blob store is scoped only to manifest link paths. Manifest link paths doesn't use blob cache
func GetManifestLinkReadOnlyBlobStore(
	ctx context.Context,
	repo distribution.Repository,
	driver storagedriver.StorageDriver,
	blobDescriptorServiceFactory distribution.BlobDescriptorServiceFactory,
) ReadOnlyBlobStore {

	manifestLinkPathFns := []linkPathFunc{
		manifestRevisionLinkPath,
	}

	manifestDirectoryPathSpec := manifestRevisionsPathSpec{name: repo.Named().Name()}

	// create global statter
	bstatter := &blobStatter{
		driver: driver,
	}

	bs := &blobStore{
		driver:  driver,
		statter: bstatter,
	}

	var statter distribution.BlobDescriptorService = &linkedBlobStatter{
		blobStore:   bs,
		repository:  repo,
		linkPathFns: manifestLinkPathFns,
	}

	if blobDescriptorServiceFactory != nil {
		statter = blobDescriptorServiceFactory.BlobAccessController(statter)
	}

	return &linkedBlobStore{
		ctx:                  ctx,
		blobStore:            bs,
		repository:           repo,
		blobAccessController: statter,

		// linkPath limits this blob store to only
		// manifests. This instance cannot be used for blob checks.
		linkPathFns:           manifestLinkPathFns,
		linkDirectoryPathSpec: manifestDirectoryPathSpec,
	}
}

// GetTagLinkReadOnlyBlobStore will enable extensions to access the underlying linked blob store for readonly operations.
// This blob store is scoped only to tag link paths. Tag link paths doesn't use blob cache
func GetTagLinkReadOnlyBlobStore(
	ctx context.Context,
	repo distribution.Repository,
	driver storagedriver.StorageDriver,
	tag string) ReadOnlyBlobStore {

	var tagLinkPath = func(name string, dgst digest.Digest) (string, error) {
		return pathFor(manifestTagIndexEntryLinkPathSpec{
			name:     name,
			tag:      tag,
			revision: dgst,
		})
	}

	// create global statter
	statter := &blobStatter{
		driver: driver,
	}

	bs := &blobStore{
		driver:  driver,
		statter: statter,
	}

	return &linkedBlobStore{
		blobStore: bs,
		blobAccessController: &linkedBlobStatter{
			blobStore:   bs,
			repository:  repo,
			linkPathFns: []linkPathFunc{manifestRevisionLinkPath},
		},
		repository: repo,
		ctx:        ctx,
		// linkPath limits this blob store to only
		// tags.
		linkPathFns: []linkPathFunc{tagLinkPath},
		linkDirectoryPathSpec: manifestTagIndexPathSpec{
			name: repo.Named().Name(),
			tag:  tag,
		},
	}
}
