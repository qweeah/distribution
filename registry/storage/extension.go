package storage

import (
	"context"
	"path"

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

// ExtendedStorage defines extensions to store operations like manifest for example.
type ExtendedStorage interface {
	// GetManifestHandlers returns the list of manifest handlers that handle custom manifest formats supported by the extensions.
	GetManifestHandlers(
		repo distribution.Repository,
		blobStore distribution.BlobStore) []ManifestHandler
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

func EnumerateReferrerLinks(ctx context.Context,
	rootPath string,
	stDriver storagedriver.StorageDriver,
	blobStatter distribution.BlobStatter,
	manifestService distribution.ManifestService,
	repositoryName string,
	markSet map[digest.Digest]struct{},
	artifactManifestIndex map[digest.Digest]ArtifactManifestDel,
	ingestor func(ctx context.Context,
		digest digest.Digest,
		manifestService distribution.ManifestService,
		markSet map[digest.Digest]struct{},
		artifactManifestIndex map[digest.Digest]ArtifactManifestDel,
		repoName string,
		storageDriver driver.StorageDriver,
		blobStatter distribution.BlobStatter) error) error {

	return stDriver.Walk(ctx, rootPath, func(fileInfo driver.FileInfo) error {
		// exit early if directory...
		if fileInfo.IsDir() {
			return nil
		}
		filePath := fileInfo.Path()

		// check if it's a link
		_, fileName := path.Split(filePath)
		if fileName != "link" {
			return nil
		}

		// read the digest found in link
		digest, err := readlink(ctx, filePath, stDriver)
		if err != nil {
			return err
		}

		// ensure this conforms to the linkPathFns
		_, err = blobStatter.Stat(ctx, digest)
		if err != nil {
			// we expect this error to occur so we move on
			if err == distribution.ErrBlobUnknown {
				return nil
			}
			return err
		}

		err = ingestor(ctx, digest, manifestService, markSet, artifactManifestIndex, repositoryName, stDriver, blobStatter)
		if err != nil {
			return err
		}

		return nil
	})
}

func readlink(ctx context.Context, path string, stDriver storagedriver.StorageDriver) (digest.Digest, error) {
	content, err := stDriver.GetContent(ctx, path)
	if err != nil {
		return "", err
	}

	linked, err := digest.Parse(string(content))
	if err != nil {
		return "", err
	}

	return linked, nil
}
