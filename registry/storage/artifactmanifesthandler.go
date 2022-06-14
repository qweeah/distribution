package storage

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"time"

	"github.com/distribution/distribution/v3"
	dcontext "github.com/distribution/distribution/v3/context"
	"github.com/distribution/distribution/v3/manifest/orasartifact"
	"github.com/distribution/distribution/v3/registry/storage/driver"
	"github.com/opencontainers/go-digest"
	v1 "github.com/oras-project/artifacts-spec/specs-go/v1"
)

var (
	errInvalidArtifactType      = errors.New("artifactType invalid")
	errInvalidMediaType         = errors.New("mediaType invalid")
	errInvalidCreatedAnnotation = errors.New("failed to parse created time")
)

// ArtifactManifestHandler is a ManifestHandler that covers ORAS Artifacts.
type ArtifactManifestHandler struct {
	Repository    distribution.Repository
	BlobStore     distribution.BlobStore
	StorageDriver driver.StorageDriver
}

func (amh *ArtifactManifestHandler) Unmarshal(ctx context.Context, dgst digest.Digest, content []byte) (distribution.Manifest, error) {
	dcontext.GetLogger(ctx).Debug("(*artifactManifestHandler).Unmarshal")

	var v json.RawMessage
	if json.Unmarshal(content, &v) != nil {
		return nil, distribution.ErrManifestFormatUnsupported
	}

	dm := &orasartifact.DeserializedManifest{}
	if err := dm.UnmarshalJSON(content); err != nil {
		return nil, distribution.ErrManifestFormatUnsupported
	}

	return dm, nil
}

func (ah *ArtifactManifestHandler) Put(ctx context.Context, man distribution.Manifest, skipDependencyVerification bool) (digest.Digest, error) {
	dcontext.GetLogger(ctx).Debug("(*artifactManifestHandler).Put")

	da, ok := man.(*orasartifact.DeserializedManifest)
	if !ok {
		return "", distribution.ErrManifestFormatUnsupported
	}

	if err := ah.verifyManifest(ctx, *da, skipDependencyVerification); err != nil {
		return "", err
	}

	mt, payload, err := da.Payload()
	if err != nil {
		return "", err
	}

	revision, err := ah.BlobStore.Put(ctx, mt, payload)
	if err != nil {
		dcontext.GetLogger(ctx).Errorf("error putting payload into blobstore: %v", err)
		return "", err
	}

	err = ah.indexReferrers(ctx, *da, revision.Digest)
	if err != nil {
		dcontext.GetLogger(ctx).Errorf("error indexing referrers: %v", err)
		return "", err
	}

	return revision.Digest, nil
}

// verifyManifest ensures that the manifest content is valid from the
// perspective of the registry. As a policy, the registry only tries to
// store valid content, leaving trust policies of that content up to
// consumers.
func (amh *ArtifactManifestHandler) verifyManifest(ctx context.Context, dm orasartifact.DeserializedManifest, skipDependencyVerification bool) error {
	var errs distribution.ErrManifestVerification

	if dm.ArtifactType() == "" {
		errs = append(errs, errInvalidArtifactType)
	}

	if dm.MediaType() != v1.MediaTypeArtifactManifest {
		errs = append(errs, errInvalidMediaType)
	}

	if createdAt, ok := dm.Annotations()[orasartifact.CreateAnnotationName]; ok {
		_, err := time.Parse(time.RFC3339, createdAt)
		if err != nil {
			errs = append(errs, errInvalidCreatedAnnotation)
		}
	}

	if !skipDependencyVerification {
		bs := amh.Repository.Blobs(ctx)

		// All references must exist.
		for _, blobDesc := range dm.References() {
			desc, err := bs.Stat(ctx, blobDesc.Digest)
			if err != nil && err != distribution.ErrBlobUnknown {
				errs = append(errs, err)
			}
			if err != nil || desc.Digest == "" {
				// On error here, we always append unknown blob errors.
				errs = append(errs, distribution.ErrManifestBlobUnknown{Digest: blobDesc.Digest})
			}
		}

		ms, err := amh.Repository.Manifests(ctx)
		if err != nil {
			return err
		}

		// Validate subject manifest.
		subject := dm.Subject()
		exists, err := ms.Exists(ctx, subject.Digest)
		if !exists || err == distribution.ErrBlobUnknown {
			errs = append(errs, distribution.ErrManifestBlobUnknown{Digest: subject.Digest})
		} else if err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) != 0 {
		return errs
	}

	return nil
}

// indexReferrers indexes the subject of the given revision in its referrers index store.
func (amh *ArtifactManifestHandler) indexReferrers(ctx context.Context, dm orasartifact.DeserializedManifest, revision digest.Digest) error {
	// [TODO] We can use artifact type in the link path to support filtering by artifact type
	//  but need to consider the max path length in different os
	//artifactType := dm.ArtifactType()
	subjectRevision := dm.Subject().Digest
	referrerRoot, err := pathFor(referrersRootPathSpec{name: amh.Repository.Named().Name()})
	if err != nil {
		return err
	}
	rootPath := path.Join(referrerRoot, subjectRevision.Algorithm().String(), subjectRevision.Hex())
	referenceLinkPath := path.Join(rootPath, revision.Algorithm().String(), revision.Hex(), "link")
	if err := amh.StorageDriver.PutContent(ctx, referenceLinkPath, []byte(revision.String())); err != nil {
		return err
	}

	return nil
}
