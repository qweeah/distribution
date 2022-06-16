package oras

import (
	"context"
	"fmt"
	"path"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/reference"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

type orasGCHandler struct {
	artifactManifestIndex map[digest.Digest]artifactManifestDel
}

type artifactManifestDel struct {
	name           string
	artifactDigest digest.Digest
}

func (gc *orasGCHandler) Mark(ctx context.Context,
	storageDriver driver.StorageDriver,
	registry distribution.Namespace,
	dryRun bool,
	removeUntagged bool) (map[digest.Digest]struct{}, error) {
	repositoryEnumerator, ok := registry.(distribution.RepositoryEnumerator)
	if !ok {
		return nil, fmt.Errorf("unable to convert Namespace to RepositoryEnumerator")
	}

	gc.artifactManifestIndex = make(map[digest.Digest]artifactManifestDel)
	// mark
	markSet := make(map[digest.Digest]struct{})
	err := repositoryEnumerator.Enumerate(ctx, func(repoName string) error {
		fmt.Printf(repoName + "\n")

		var err error
		named, err := reference.WithName(repoName)
		if err != nil {
			return fmt.Errorf("failed to parse repo name %s: %v", repoName, err)
		}
		repository, err := registry.Repository(ctx, named)
		if err != nil {
			return fmt.Errorf("failed to construct repository: %v", err)
		}

		manifestService, err := repository.Manifests(ctx)
		if err != nil {
			return fmt.Errorf("failed to construct manifest service: %v", err)
		}

		manifestEnumerator, ok := manifestService.(distribution.ManifestEnumerator)
		if !ok {
			return fmt.Errorf("unable to convert ManifestService into ManifestEnumerator")
		}

		err = manifestEnumerator.Enumerate(ctx, func(dgst digest.Digest) error {
			manifest, err := manifestService.Get(ctx, dgst)
			if err != nil {
				return fmt.Errorf("failed to retrieve manifest for digest %v: %v", dgst, err)
			}

			mediaType, _, err := manifest.Payload()
			if err != nil {
				return err
			}

			// if the manifest is an oras artifact, skip it
			// the artifact marking occurs when walking the refs
			if mediaType == v1.MediaTypeArtifactManifest {
				return nil
			}

			blobStatter := registry.BlobStatter()
			referrerRootPath := referrersLinkPath(repoName)
			if removeUntagged {
				// fetch all tags where this manifest is the latest one
				tags, err := repository.Tags(ctx).Lookup(ctx, distribution.Descriptor{Digest: dgst})
				if err != nil {
					return fmt.Errorf("failed to retrieve tags for digest %v: %v", dgst, err)
				}
				if len(tags) == 0 {

					// find all artifacts linked to manifest and add to artifactManifestIndex for subsequent deletion
					rootPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
					err = enumerateReferrerLinks(ctx,
						rootPath,
						storageDriver,
						blobStatter,
						manifestService,
						repository.Named().Name(),
						markSet,
						gc.artifactManifestIndex,
						artifactSweepIngestor)

					if err != nil {
						switch err.(type) {
						case driver.PathNotFoundError:
							return nil
						}
						return err
					}
					return nil
				}
			}

			// recurse child artifact as subject to find lower level referrers
			rootPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
			err = enumerateReferrerLinks(ctx,
				rootPath,
				storageDriver,
				blobStatter,
				manifestService,
				repository.Named().Name(),
				markSet,
				gc.artifactManifestIndex,
				artifactMarkIngestor)

			if err != nil {
				switch err.(type) {
				case driver.PathNotFoundError:
					return nil
				}
				return err
			}
			return nil
		})

		// In certain situations such as unfinished uploads, deleting all
		// tags in S3 or removing the _manifests folder manually, this
		// error may be of type PathNotFound.
		//
		// In these cases we can continue marking other manifests safely.
		if _, ok := err.(driver.PathNotFoundError); ok {
			return nil
		}

		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to mark: %v", err)
	}
	return markSet, nil
}

func (gc *orasGCHandler) Sweep(ctx context.Context,
	storageDriver driver.StorageDriver,
	registry distribution.Namespace,
	dryRun bool,
	removeUntagged bool) error {
	vacuum := storage.NewVacuum(ctx, storageDriver)
	if !dryRun {
		// remove each artifact in the index
		for artifactDigest, obj := range gc.artifactManifestIndex {
			err := vacuum.RemoveArtifactManifest(obj.name, artifactDigest)
			if err != nil {
				return fmt.Errorf("failed to delete artifact manifest %s: %v", artifactDigest, err)
			}
		}
	}

	return nil
}

// ingestor method used in EnumerateReferrerLinks
// marks each artifact manifest and associated blobs
func artifactMarkIngestor(ctx context.Context,
	referrerRevision digest.Digest,
	manifestService distribution.ManifestService,
	markSet map[digest.Digest]struct{},
	artifactManifestIndex map[digest.Digest]artifactManifestDel,
	repoName string,
	storageDriver driver.StorageDriver,
	blobStatter distribution.BlobStatter) error {
	man, err := manifestService.Get(ctx, referrerRevision)
	if err != nil {
		return err
	}

	// mark the artifact manifest blob
	fmt.Printf("%s: marking artifact manifest %s\n", repoName, referrerRevision.String())
	markSet[referrerRevision] = struct{}{}

	// mark the artifact blobs
	descriptors := man.References()
	for _, descriptor := range descriptors {
		markSet[descriptor.Digest] = struct{}{}
		fmt.Printf("%s: marking blob %s\n", repoName, descriptor.Digest)
	}
	referrerRootPath := referrersLinkPath(repoName)

	rootPath := path.Join(referrerRootPath, referrerRevision.Algorithm().String(), referrerRevision.Hex())
	_, err = storageDriver.Stat(ctx, rootPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return enumerateReferrerLinks(ctx, rootPath, storageDriver, blobStatter, manifestService, repoName, markSet, artifactManifestIndex, artifactMarkIngestor)
}

// ingestor method used in EnumerateReferrerLinks
// indexes each artifact manifest and adds ArtifactManifestDel struct to index
func artifactSweepIngestor(ctx context.Context,
	referrerRevision digest.Digest,
	manifestService distribution.ManifestService,
	markSet map[digest.Digest]struct{},
	artifactManifestIndex map[digest.Digest]artifactManifestDel,
	repoName string,
	storageDriver driver.StorageDriver,
	blobStatter distribution.BlobStatter) error {

	// index the manifest
	fmt.Printf("%s: indexing artifact manifest %s\n", repoName, referrerRevision.String())
	artifactManifestIndex[referrerRevision] = artifactManifestDel{name: repoName, artifactDigest: referrerRevision}

	referrerRootPath := referrersLinkPath(repoName)

	rootPath := path.Join(referrerRootPath, referrerRevision.Algorithm().String(), referrerRevision.Hex())
	_, err := storageDriver.Stat(ctx, rootPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return enumerateReferrerLinks(ctx, rootPath, storageDriver, blobStatter, manifestService, repoName, markSet, artifactManifestIndex, artifactSweepIngestor)
}
