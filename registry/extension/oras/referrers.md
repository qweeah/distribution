# ORAS Artifacts Distribution

This document describes an experimental prototype that implements the
[ORAS Artifact Manifest](https://github.com/oras-project/artifacts-spec) spec.

## Implementation

To power the [/referrers](https://github.com/oras-project/artifacts-spec/blob/main/manifest-referrers-api.md) API, the
referrers of a manifest are indexed in the repository store. The following example illustrates the creation of this
index.

The `nginx:v1` image is already persisted:

- repository: `nginx`
- digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- tag: `v1.0`

The repository store layout is represented as:

```bash
<root>
└── v2
    └── repositories
        └── nginx
            └── _manifests
                └── revisions
                    └── sha256
                        └── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                            └── link
```

Push a signature as blob and an ORAS Artifact that contains a blobs property referencing the signature, with the
following properties:

- digest: `sha256:222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i`
- `subjectManifest` digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- `artifactType`: `application/vnd.example.artifact`

On `PUT`, the artifact appears as a manifest revision. Additionally, an index entry is created under
the subject ref folder to facilitate a lookup to the referrer. The index path where the entry is added is
`<repository>/_refs/subjects/sha256/<subject-digest>`, as shown below.

```
<root>
└── v2
    └── repositories
        └── nginx
            ├── _manifests
            │   └── _revisions
            │       └── sha256
            │           ├── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
            │           │   └── link
            │           └── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
            │               └── link
            └── _refs
                └── subjects
                    └── sha256
                        └── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                            └── sha256
                                └── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                                    └── link
```

Push another ORAS artifact with the following properties:

- digest: `sha256:333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i`
- `subjectManifest` digest: `sha256:111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m`
- `artifactType`: `application/vnd.another.example.artifact`

This results in an addition to the index as shown below.

```
<root>
└── v2
    └── repositories
        └── nginx
            ├── _manifests
            │   └── _revisions
            │       └── sha256
            │           ├── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
            │           │   └── link
            │           ├── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
            │           │   └── link
            │           └── 333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i
            │               └── link
            └── _refs
                └── subjects
                    └── sha256
                        └── 111ma2d22ae5ef400769fa51c84717264cd1520ac8d93dc071374c1be49a111m
                            └── sha256
                                ├── 222ibbf80b44ce6be8234e6ff90a1ac34acbeb826903b02cfa0da11c82cb222i
                                │   └── link
                                └── 333ic0c33ebc4a74a0a554c86ac2b28ddf3454a5ad9cf90ea8cea9f9e75c333i
                                    └── link
```

## Garbage Collection With Referrers

The life of a referrer artifact is directly linked to its subject. When a referrer artifact's subject manifest is deleted, the artifact's referrers are also deleted. 

Manifest garbage collection is extended to include referrer artifact collection. The marking process begins with the normal marking behavior which consists of enumerating every manifest in every repository. If the manifest is untagged, we must consider the manifest for deletion. As we cannot guarantee that artifact manifests (tagged or untagged) will be traversed before their subjects, we must temporarily mark all artifact manifests and their blobs using a separate reference count map. If we encounter an untagged non-artifact manifest, then we proceed by adding the manifest to a deletion list, traversing it's referrers, and then indexing each artifact manifest for deletion.

During the Sweep phase, each manifest in the deletion list has its contents and link files deleted. Then each of the indexed artifact manifests referring to the deleted subject will have its corresponding manifest and blobs' ref counts decremented. Furthermore, the artifact manifest revision, and `_refs` directories are removed. The final step is the vacuum of the blobs. Based on the final ref count map, we add each blob with a positive ref count back to the original `markSet` map. All unmarked blobs are then safely deleted.