package storage

import (
	"context"
	"fmt"
	"path"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/reference"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

func emit(format string, a ...interface{}) {
	fmt.Printf(format+"\n", a...)
}

// GCOpts contains options for garbage collector
type GCOpts struct {
	DryRun         bool
	RemoveUntagged bool
}

// ManifestDel contains manifest structure which will be deleted
type ManifestDel struct {
	Name   string
	Digest digest.Digest
	Tags   []string
}

// ArtifactManifestDel contains artifact manifest structure which will be deleted
type ArtifactManifestDel struct {
	Name           string
	ArtifactDigest digest.Digest
}

// MarkAndSweep performs a mark and sweep of registry data
func MarkAndSweep(ctx context.Context, storageDriver driver.StorageDriver, registry distribution.Namespace, opts GCOpts) error {
	repositoryEnumerator, ok := registry.(distribution.RepositoryEnumerator)
	if !ok {
		return fmt.Errorf("unable to convert Namespace to RepositoryEnumerator")
	}

	// mark
	markSet := make(map[digest.Digest]struct{})
	manifestArr := make([]ManifestDel, 0)
	artifactManifestIndex := make(map[digest.Digest]ArtifactManifestDel)
	err := repositoryEnumerator.Enumerate(ctx, func(repoName string) error {
		emit(repoName)

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
			referrerRootPath, err := pathFor(referrersRootPathSpec{name: repository.Named().Name()})
			if err != nil {
				return err
			}

			if opts.RemoveUntagged {
				// fetch all tags where this manifest is the latest one
				tags, err := repository.Tags(ctx).Lookup(ctx, distribution.Descriptor{Digest: dgst})
				if err != nil {
					return fmt.Errorf("failed to retrieve tags for digest %v: %v", dgst, err)
				}
				if len(tags) == 0 {
					emit("manifest eligible for deletion: %s", dgst)
					// fetch all tags from repository
					// all of these tags could contain manifest in history
					// which means that we need check (and delete) those references when deleting manifest
					allTags, err := repository.Tags(ctx).All(ctx)
					if err != nil {
						return fmt.Errorf("failed to retrieve tags %v", err)
					}
					manifestArr = append(manifestArr, ManifestDel{Name: repoName, Digest: dgst, Tags: allTags})

					// find all artifacts linked to manifest and add to artifactManifestIndex for subsequent deletion
					rootPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
					err = EnumerateReferrerLinks(ctx,
						rootPath,
						storageDriver,
						blobStatter,
						manifestService,
						repository.Named().Name(),
						markSet,
						artifactManifestIndex,
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
			// Mark the manifest's blob
			emit("%s: marking manifest %s ", repoName, dgst)
			markSet[dgst] = struct{}{}

			descriptors := manifest.References()
			for _, descriptor := range descriptors {
				markSet[descriptor.Digest] = struct{}{}
				emit("%s: marking blob %s", repoName, descriptor.Digest)
			}

			// recurse child artifact as subject to find lower level referrers
			rootPath := path.Join(referrerRootPath, dgst.Algorithm().String(), dgst.Hex())
			err = EnumerateReferrerLinks(ctx,
				rootPath,
				storageDriver,
				blobStatter,
				manifestService,
				repository.Named().Name(),
				markSet,
				artifactManifestIndex,
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
		return fmt.Errorf("failed to mark: %v", err)
	}

	// sweep
	vacuum := NewVacuum(ctx, storageDriver)
	if !opts.DryRun {
		for _, obj := range manifestArr {
			err = vacuum.RemoveManifest(obj.Name, obj.Digest, obj.Tags)
			if err != nil {
				return fmt.Errorf("failed to delete manifest %s: %v", obj.Digest, err)
			}
		}
		// remove each artifact in the index
		for artifactDigest, obj := range artifactManifestIndex {
			err = vacuum.RemoveArtifactManifest(obj.Name, artifactDigest)
			if err != nil {
				return fmt.Errorf("failed to delete artifact manifest %s: %v", artifactDigest, err)
			}
		}
	}
	blobService := registry.Blobs()
	deleteSet := make(map[digest.Digest]struct{})
	err = blobService.Enumerate(ctx, func(dgst digest.Digest) error {
		// check if digest is in markSet. If not, delete it!
		if _, ok := markSet[dgst]; !ok {
			deleteSet[dgst] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("error enumerating blobs: %v", err)
	}
	emit("\n%d blobs marked, %d blobs and %d manifests eligible for deletion", len(markSet), len(deleteSet), len(manifestArr))
	for dgst := range deleteSet {
		emit("blob eligible for deletion: %s", dgst)
		if opts.DryRun {
			continue
		}
		err = vacuum.RemoveBlob(string(dgst))
		if err != nil {
			return fmt.Errorf("failed to delete blob %s: %v", dgst, err)
		}
	}

	return err
}

// ingestor method used in EnumerateReferrerLinks
// marks each artifact manifest and associated blobs
func artifactMarkIngestor(ctx context.Context,
	referrerRevision digest.Digest,
	manifestService distribution.ManifestService,
	markSet map[digest.Digest]struct{},
	artifactManifestIndex map[digest.Digest]ArtifactManifestDel,
	repoName string,
	storageDriver driver.StorageDriver,
	blobStatter distribution.BlobStatter) error {
	man, err := manifestService.Get(ctx, referrerRevision)
	if err != nil {
		return err
	}

	// mark the artifact manifest blob
	emit("%s: marking artifact manifest %s ", repoName, referrerRevision.String())
	markSet[referrerRevision] = struct{}{}

	// mark the artifact blobs
	descriptors := man.References()
	for _, descriptor := range descriptors {
		markSet[descriptor.Digest] = struct{}{}
		emit("%s: marking blob %s", repoName, descriptor.Digest)
	}
	referrerRootPath, err := pathFor(referrersRootPathSpec{name: repoName})
	if err != nil {
		return err
	}
	rootPath := path.Join(referrerRootPath, referrerRevision.Algorithm().String(), referrerRevision.Hex())
	_, err = storageDriver.Stat(ctx, rootPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return EnumerateReferrerLinks(ctx, rootPath, storageDriver, blobStatter, manifestService, repoName, markSet, artifactManifestIndex, artifactMarkIngestor)
}

// ingestor method used in EnumerateReferrerLinks
// indexes each artifact manifest and adds ArtifactManifestDel struct to index
func artifactSweepIngestor(ctx context.Context,
	referrerRevision digest.Digest,
	manifestService distribution.ManifestService,
	markSet map[digest.Digest]struct{},
	artifactManifestIndex map[digest.Digest]ArtifactManifestDel,
	repoName string,
	storageDriver driver.StorageDriver,
	blobStatter distribution.BlobStatter) error {

	// index the manifest
	emit("%s: indexing artifact manifest %s ", repoName, referrerRevision.String())
	artifactManifestIndex[referrerRevision] = ArtifactManifestDel{Name: repoName, ArtifactDigest: referrerRevision}

	referrerRootPath, err := pathFor(referrersRootPathSpec{name: repoName})
	if err != nil {
		return err
	}
	rootPath := path.Join(referrerRootPath, referrerRevision.Algorithm().String(), referrerRevision.Hex())
	_, err = storageDriver.Stat(ctx, rootPath)
	if err != nil {
		switch err.(type) {
		case driver.PathNotFoundError:
			return nil
		}
		return err
	}
	return EnumerateReferrerLinks(ctx, rootPath, storageDriver, blobStatter, manifestService, repoName, markSet, artifactManifestIndex, artifactMarkIngestor)
}
