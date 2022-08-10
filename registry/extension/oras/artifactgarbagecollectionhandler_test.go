package oras

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/distribution/distribution/v3"
	"github.com/distribution/distribution/v3/manifest"
	"github.com/distribution/distribution/v3/manifest/schema2"
	"github.com/distribution/distribution/v3/registry/storage"
	"github.com/distribution/distribution/v3/registry/storage/driver/inmemory"
	"github.com/opencontainers/go-digest"
	orasartifacts "github.com/oras-project/artifacts-spec/specs-go/v1"
)

func allManifests(t *testing.T, manifestService distribution.ManifestService) map[digest.Digest]struct{} {
	ctx := context.Background()
	allManMap := make(map[digest.Digest]struct{})
	manifestEnumerator, ok := manifestService.(distribution.ManifestEnumerator)
	if !ok {
		t.Fatalf("unable to convert ManifestService into ManifestEnumerator")
	}
	err := manifestEnumerator.Enumerate(ctx, func(dgst digest.Digest) error {
		allManMap[dgst] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Error getting all manifests: %v", err)
	}
	return allManMap
}

func allBlobs(t *testing.T, registry distribution.Namespace) map[digest.Digest]struct{} {
	ctx := context.Background()
	blobService := registry.Blobs()
	allBlobsMap := make(map[digest.Digest]struct{})
	err := blobService.Enumerate(ctx, func(dgst digest.Digest) error {
		allBlobsMap[dgst] = struct{}{}
		return nil
	})
	if err != nil {
		t.Fatalf("Error getting all blobs: %v", err)
	}
	return allBlobsMap
}

func TestReferrersBlobsDeleted(t *testing.T) {
	ctx := context.Background()
	inmemoryDriver := inmemory.New()
	registry, orasExtension := createRegistry(t, inmemoryDriver)
	repo := makeRepository(t, registry, "test")
	manifestService := makeManifestService(t, repo)
	tagService := repo.Tags(ctx)

	artifactBlob, err := repo.Blobs(ctx).Put(ctx, orasartifacts.MediaTypeDescriptor, nil)
	if err != nil {
		t.Fatal(err)
	}

	config, err := repo.Blobs(ctx).Put(ctx, schema2.MediaTypeImageConfig, nil)
	if err != nil {
		t.Fatal(err)
	}

	layer, err := repo.Blobs(ctx).Put(ctx, schema2.MediaTypeLayer, nil)
	if err != nil {
		t.Fatal(err)
	}

	subjectManifest := schema2.Manifest{
		Versioned: manifest.Versioned{
			SchemaVersion: 2,
			MediaType:     schema2.MediaTypeManifest,
		},
		Config: config,
		Layers: []distribution.Descriptor{
			layer,
		},
	}

	dm, err := schema2.FromStruct(subjectManifest)
	if err != nil {
		t.Fatalf("failed to marshal subject manifest: %v", err)
	}
	_, dmPayload, err := dm.Payload()
	if err != nil {
		t.Fatalf("failed to get subject manifest payload: %v", err)
	}

	dg, err := manifestService.Put(ctx, dm)
	if err != nil {
		t.Fatalf("failed to put subject manifest with err: %v", err)
	}

	artifactBlobDescriptor := orasartifacts.Descriptor{
		MediaType: artifactBlob.MediaType,
		Digest:    artifactBlob.Digest,
		Size:      artifactBlob.Size,
	}

	artifactManifest := orasartifacts.Manifest{
		MediaType:    orasartifacts.MediaTypeArtifactManifest,
		ArtifactType: "test_artifactType",
		Blobs: []orasartifacts.Descriptor{
			artifactBlobDescriptor,
		},
		Subject: &orasartifacts.Descriptor{
			MediaType: schema2.MediaTypeManifest,
			Size:      int64(len(dmPayload)),
			Digest:    dg,
		},
	}

	marshalledMan, err := json.Marshal(artifactManifest)
	if err != nil {
		t.Fatalf("artifact manifest could not be serialized to byte array: %v", err)
	}
	// upload manifest
	artifactManifestDigest, err := manifestService.Put(ctx, &DeserializedManifest{
		Manifest: Manifest{
			inner: artifactManifest,
		},
		raw: marshalledMan,
	})
	if err != nil {
		t.Fatalf("artifact manifest upload failed: %v", err)
	}

	// the tags folder doesn't exist for this repo until a tag is added
	// this leads to an error in Mark and Sweep if tags folder not found
	err = tagService.Tag(ctx, "test", distribution.Descriptor{Digest: dg})
	if err != nil {
		t.Fatalf("failed to tag subject image: %v", err)
	}
	err = tagService.Untag(ctx, "test")
	if err != nil {
		t.Fatalf("failed to untag subject image: %v", err)
	}

	// Run GC
	err = storage.MarkAndSweep(ctx, inmemoryDriver, registry, storage.GCOpts{
		DryRun:              false,
		RemoveUntagged:      true,
		GCExtensionHandlers: orasExtension.GetGarbageCollectionHandlers(),
	})
	if err != nil {
		t.Fatalf("Failed mark and sweep: %v", err)
	}

	manifests := allManifests(t, manifestService)
	blobs := allBlobs(t, registry)

	if _, exists := manifests[artifactManifestDigest]; exists {
		t.Fatalf("artifact manifest with digest %s should have been deleted", artifactManifestDigest.String())
	}

	if _, exists := blobs[artifactManifestDigest]; exists {
		t.Fatalf("artifact manifest blob with digest %s should have been deleted", artifactManifestDigest.String())
	}

	blobDigest := artifactManifest.Blobs[0].Digest
	if _, exists := blobs[blobDigest]; exists {
		t.Fatalf("artifact blob with digest %s should have been deleted", blobDigest)
	}
}
