package oras

import (
	"context"
	"fmt"
	"path"

	"github.com/distribution/distribution/v3"
	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/reference"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
	artifactv1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

type orasGCHandler struct {
	artifactManifestIndex map[digest.Digest][]digest.Digest
	artifactMarkSet       map[digest.Digest]int
}

func (gc *orasGCHandler) Mark(ctx context.Context,
	repository distribution.Repository,
	storageDriver driver.StorageDriver,
	registry distribution.Namespace,
	manifest distribution.Manifest,
	dgst digest.Digest,
	dryRun bool,
	removeUntagged bool) (bool, error) {
	//markSet := make(map[digest.Digest]struct{})
	blobStatter := registry.BlobStatter()
	mediaType, _, err := manifest.Payload()
	if err != nil {
		return false, err
	}
	referrerRootPath := referrersLinkPath(repository.Named().Name())
	rootPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())

	if mediaType == artifactv1.MediaTypeArtifactManifest {
		// if the manifest passed is an artifact -> mark the manifest and blobs for now
		fmt.Printf("%s: incrementing artifact manifest ref count %s\n", repository.Named().Name(), dgst.String())
		gc.artifactMarkSet[dgst] += 1

		// mark the artifact blobs
		descriptors := manifest.References()
		for _, descriptor := range descriptors {
			gc.artifactMarkSet[descriptor.Digest] += 1
			fmt.Printf("%s: incrementing artifact blob ref count %s\n", repository.Named().Name(), descriptor.Digest)
		}
		return false, nil
	} else {
		// if the manifest passed isn't an an artifact -> call the sweep ingestor
		// find all artifacts linked to manifest and add to artifactManifestIndex for subsequent deletion
		gc.artifactManifestIndex[dgst] = make([]digest.Digest, 0)
		err := enumerateReferrerLinks(ctx,
			rootPath,
			storageDriver,
			repository,
			blobStatter,
			dgst,
			gc.artifactManifestIndex,
			artifactSweepIngestor)

		if err != nil {
			switch err.(type) {
			case driver.PathNotFoundError:
				return true, nil
			}
			return true, err
		}
		return true, nil
	}
}

func (gc *orasGCHandler) RemoveManifest(ctx context.Context, storageDriver driver.StorageDriver, registry distribution.Namespace, dgst digest.Digest, repositoryName string) error {
	referrerRootPath := referrersLinkPath(repositoryName)
	fullArtifactManifestPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
	dcontext.GetLogger(ctx).Infof("deleting manifest ref folder: %s", fullArtifactManifestPath)
	err := storageDriver.Delete(ctx, fullArtifactManifestPath)
	if err != nil {
		if _, ok := err.(driver.PathNotFoundError); !ok {
			return err
		}
	}

	subjectLinkedArtifacts, ok := gc.artifactManifestIndex[dgst]
	if ok {
		for _, artifactDigest := range subjectLinkedArtifacts {
			// get the artifact manifest
			named, err := reference.WithName(repositoryName)
			if err != nil {
				return fmt.Errorf("failed to parse repo name %s: %v", repositoryName, err)
			}
			repository, err := registry.Repository(ctx, named)
			if err != nil {
				return fmt.Errorf("failed to construct repository: %v", err)
			}

			manifestService, err := repository.Manifests(ctx)
			if err != nil {
				return fmt.Errorf("failed to construct manifest service: %v", err)
			}
			artifactManifest, err := manifestService.Get(ctx, artifactDigest)
			if err != nil {
				return fmt.Errorf("failed to get artifact manifest: %v", err)
			}

			// extract the reference
			blobs := artifactManifest.References()

			// decrement refcount for the blobs digests' and the manifest digest
			gc.artifactMarkSet[artifactDigest] -= 1
			fmt.Printf("%s: decrementing artifact manifest ref count %s\n", repositoryName, dgst)
			for _, descriptor := range blobs {
				gc.artifactMarkSet[descriptor.Digest] -= 1
				fmt.Printf("%s: decrementing artifact blob ref count %s\n", repositoryName, descriptor.Digest)
			}
			// delete each artifact manifest's revision
			manifestPath := referrersRepositoriesManifestRevisionPath(repositoryName, artifactDigest)
			dcontext.GetLogger(ctx).Infof("deleting artifact manifest revision: %s", manifestPath)
			err = storageDriver.Delete(ctx, manifestPath)
			if err != nil {
				if _, ok := err.(driver.PathNotFoundError); !ok {
					return err
				}
			}
			// delete each artifact manifest's ref folder
			fullArtifactManifestPath = path.Join(referrerRootPath, artifactDigest.Algorithm().String(), artifactDigest.Hex())
			dcontext.GetLogger(ctx).Infof("deleting artifact manifest ref folder: %s", fullArtifactManifestPath)
			err = storageDriver.Delete(ctx, fullArtifactManifestPath)
			if err != nil {
				if _, ok := err.(driver.PathNotFoundError); !ok {
					return err
				}
			}
		}
	}

	return nil
}

func (gc *orasGCHandler) SweepBlobs(ctx context.Context, markSet map[digest.Digest]struct{}) map[digest.Digest]struct{} {
	for key, refCount := range gc.artifactMarkSet {
		if refCount > 0 {
			markSet[key] = struct{}{}
		}
	}
	return markSet
}

// ingestor method used in EnumerateReferrerLinks
// indexes each artifact manifest and adds ArtifactManifestDel struct to index
func artifactSweepIngestor(ctx context.Context,
	referrerRevision digest.Digest,
	subjectRevision digest.Digest,
	artifactManifestIndex map[digest.Digest][]digest.Digest,
	repository distribution.Repository,
	blobstatter distribution.BlobStatter,
	storageDriver driver.StorageDriver) error {
	repoName := repository.Named().Name()
	// index the manifest
	fmt.Printf("%s: indexing artifact manifest %s\n", repoName, referrerRevision.String())
	// if artifact is tagged, we don't add artifact and descendants to artifact manifest index
	tags, err := repository.Tags(ctx).Lookup(ctx, distribution.Descriptor{Digest: referrerRevision})
	if err != nil {
		return fmt.Errorf("failed to retrieve tags for artifact digest %v: %v", referrerRevision, err)
	}
	if len(tags) > 0 {
		return nil
	}
	artifactManifestIndex[subjectRevision] = append(artifactManifestIndex[subjectRevision], referrerRevision)

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
	return enumerateReferrerLinks(ctx,
		rootPath,
		storageDriver,
		repository,
		blobstatter,
		subjectRevision,
		artifactManifestIndex,
		artifactSweepIngestor)
}
